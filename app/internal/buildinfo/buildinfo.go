// Package buildinfo 保存由 -ldflags 注入的构建信息。
package buildinfo

// 由发布流水线注入：
//
//	-X gqy/internal/buildinfo.Version=v0.2.0
//	-X gqy/internal/buildinfo.Commit=<sha>
//	-X gqy/internal/buildinfo.Date=<RFC3339>
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String 返回便于日志展示的版本串。
func String() string {
	if Commit == "" || Commit == "none" {
		return Version
	}
	if len(Commit) > 12 {
		return Version + " (" + Commit[:12] + ")"
	}
	return Version + " (" + Commit + ")"
}
