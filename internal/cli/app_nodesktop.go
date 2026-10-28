//go:build !desktop

package cli

import (
	"context"
	"errors"
)

// HasDesktop reports whether this build includes the native app.
const HasDesktop = false

func (ap *app) cmdApp(ctx context.Context, _ []string) error {
	return errors.New("这个 dsdock 没有包含桌面界面：请下载 DSDock.app，或者用 dsdock gui 在浏览器里打开")
}
