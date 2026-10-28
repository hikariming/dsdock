package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hikariming/dsdock/internal/gui"
)

func (ap *app) cmdGUI(ctx context.Context, _ []string) error {
	port := 0
	if p := ap.a.str("port"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 65535 {
			return usageError("--port 需要一个端口号")
		}
		port = n
	}
	ap.core.SetProgress(nil) // jobs report progress in the page instead
	srv := gui.New(ap.core, Version)
	defer srv.Close() // a DSH web started from the page goes with it
	return srv.Serve(ctx, port, func(url string) {
		fmt.Fprintf(ap.out, "DSDock 已在本机启动：\n  %s\n（地址里带有这次运行的访问令牌，不要分享给别人。按 Ctrl+C 退出）\n", url)
		if !ap.a.is("no-open") {
			if err := gui.Open(url); err != nil {
				fmt.Fprintln(ap.errw, "  没能自动打开浏览器，请手动打开上面的地址")
			}
		}
	})
}
