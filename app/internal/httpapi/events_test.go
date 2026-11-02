package httpapi

import "testing"

// 服务退出时先关 SSE 长连接：订阅者收到关闭信号就会返回，Shutdown 不用等到超时。
func TestEventHubCloseAll(t *testing.T) {
	h := newEventHub()
	first := h.subscribe()
	second := h.subscribe()

	h.closeAll()

	for _, ch := range []chan []byte{first, second} {
		if _, ok := <-ch; ok {
			t.Fatal("closeAll 之后订阅通道应当关闭")
		}
	}
	// handler 的 defer unsubscribe 对已经关掉的通道仍然安全。
	h.unsubscribe(first)
}

// 关掉旧连接之后，新订阅照常工作。
func TestEventHubSubscribeAfterCloseAll(t *testing.T) {
	h := newEventHub()
	h.subscribe()
	h.closeAll()

	ch := h.subscribe()
	h.publish("config", map[string]any{"version": 3})
	select {
	case payload, ok := <-ch:
		if !ok {
			t.Fatal("新订阅不应被关闭")
		}
		if len(payload) == 0 {
			t.Fatal("发布的内容不应为空")
		}
	default:
		t.Fatal("新订阅应当收到发布的内容")
	}
}
