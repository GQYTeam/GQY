package gateway

import (
	"context"
	"testing"
	"time"

	"gqy/internal/config"
	"gqy/internal/provider"
)

// tenantSnapshot 构造带租户配置的快照（MT1/MT3/MT7）。
func tenantSnapshot(tenants []config.Tenant, queueTimeoutMS int) *config.Snapshot {
	snap := testRouterSnapshot([]config.Provider{testProvider("p1", "m1", testAccount("a1"))}, nil)
	snap.Routing.Tenants = tenants
	snap.Routing.QueueTimeoutMS = queueTimeoutMS
	return snap
}

// TestTenantConcurrencyIsolation 验证：一个租户打满并发不会拖慢另一个租户（MT3）。
func TestTenantConcurrencyIsolation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var first bool
	fake := &fakeAdapter{chatFn: func(ctx context.Context, _ provider.Endpoint, _ *provider.Request) (*provider.Response, error) {
		if !first {
			first = true
			close(entered)
			<-release
		}
		return &provider.Response{Text: "ok", StopReason: provider.StopEndTurn}, nil
	}}
	snap := tenantSnapshot([]config.Tenant{
		{Name: "A", MaxConcurrency: 1},
		{Name: "B", MaxConcurrency: 1},
	}, 100)
	home := t.TempDir()
	r := newTestRouter(t, home, fake)
	defer r.Close()

	// A 占住自己唯一的并发槽（适配器内阻塞）。
	doneA := make(chan error, 1)
	go func() {
		_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{Tenant: "A"})
		doneA <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("A 的第一条请求未进入适配器")
	}

	// B 不受 A 影响：应立即成功。
	start := time.Now()
	if _, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{Tenant: "B"}); err != nil {
		t.Fatalf("B 应独立成功: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("B 被 A 拖慢: %v", elapsed)
	}

	// A 的第二条请求应排队超时（tenant_queue_timeout），而不是无限等待。
	_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{Tenant: "A"})
	if pe := provider.AsError(err); pe.Code != provider.CodeTenantQueueTimeout {
		t.Fatalf("A 第二条应 tenant_queue_timeout，实际 %v", err)
	}

	close(release)
	if err := <-doneA; err != nil {
		t.Fatalf("A 第一条应成功: %v", err)
	}
}

// TestTenantQuotaPerMinute 验证每分钟请求配额与错误码（MT3）。
func TestTenantQuotaPerMinute(t *testing.T) {
	fake := &fakeAdapter{}
	snap := tenantSnapshot([]config.Tenant{{Name: "Q", ReqPerMin: 1}}, 500)
	r := newTestRouter(t, t.TempDir(), fake)
	defer r.Close()

	if _, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{Tenant: "Q"}); err != nil {
		t.Fatalf("第一条应放行: %v", err)
	}
	_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{Tenant: "Q"})
	pe := provider.AsError(err)
	if pe.Code != provider.CodeTenantQuota || pe.ClientStatus() != 429 {
		t.Fatalf("第二条应 tenant_quota/429，实际 %v", err)
	}
	if pe.RetryAfter <= 0 || pe.RetryAfter > 60 {
		t.Fatalf("RetryAfter 应给出建议秒数，实际 %d", pe.RetryAfter)
	}
}

// TestTenantUnknownRejected 验证配置了租户后，未登记名字被拒绝（MT3）。
func TestTenantUnknownRejected(t *testing.T) {
	fake := &fakeAdapter{}
	snap := tenantSnapshot([]config.Tenant{{Name: "known"}}, 500)
	r := newTestRouter(t, t.TempDir(), fake)
	defer r.Close()

	_, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{Tenant: "ghost"})
	if pe := provider.AsError(err); pe.Code != provider.CodeTenantUnknown || pe.ClientStatus() != 400 {
		t.Fatalf("未登记租户应 tenant_unknown/400，实际 %v", err)
	}
	// 空租户名落 default，不报错。
	if _, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{}); err != nil {
		t.Fatalf("default 租户应可用: %v", err)
	}
}

// TestTenantStatuses 验证观测快照（MT8）。
func TestTenantStatuses(t *testing.T) {
	fake := &fakeAdapter{}
	snap := tenantSnapshot([]config.Tenant{{Name: "S", MaxConcurrency: 2, ReqPerMin: 10}}, 500)
	r := newTestRouter(t, t.TempDir(), fake)
	defer r.Close()

	for i := 0; i < 3; i++ {
		if _, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{Tenant: "S"}); err != nil {
			t.Fatal(err)
		}
	}
	statuses := r.TenantStatuses()
	if len(statuses) != 1 {
		t.Fatalf("应有 1 个租户快照，实际 %d", len(statuses))
	}
	st := statuses[0]
	if st.Name != "S" || st.Accepted != 3 || st.MaxConc != 2 || st.Limit != 10 || st.InFlight != 0 {
		t.Fatalf("租户快照 = %+v", st)
	}
}

// BenchmarkCompleteParallel 记录多租户并发下的单请求开销（MT5）。
func BenchmarkCompleteParallel(b *testing.B) {
	fake := &fakeAdapter{}
	snap := tenantSnapshot([]config.Tenant{{Name: "bench", MaxConcurrency: 64, ReqPerMin: 0}}, 500)
	r := newTestRouterB(b, fake)
	defer r.Close()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := r.Complete(context.Background(), snap, &provider.Request{Model: "p1/m1"}, Meta{Tenant: "bench"}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func newTestRouterB(b *testing.B, fake *fakeAdapter) *Router {
	b.Helper()
	r := New(provider.NewRegistry(fake), nil, b.TempDir())
	r.now = func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }
	return r
}
