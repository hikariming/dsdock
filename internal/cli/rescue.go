package cli

import (
	"context"
	"fmt"

	"github.com/hikariming/dsdock/internal/proc"
	"github.com/hikariming/dsdock/internal/rescue"
)

func (ap *app) cmdRescue(ctx context.Context, _ []string) error {
	name, err := ap.profileName()
	if err != nil {
		return err
	}
	if _, err := ap.core.LoadProfile(name); err != nil {
		return err
	}
	ap.printf("诊断 %s …\n", name)
	d, err := rescue.Diagnose(ctx, ap.engine, name, rescue.Options{Boot: !ap.a.is("no-boot"), SmokeOpts: proc.DefaultSmoke})
	if err != nil {
		return err
	}
	if !ap.a.is("json") {
		ap.printDiagnosis(d)
	}
	if d.Healthy {
		if ap.a.is("json") {
			ap.json(d)
		}
		return nil
	}
	if d.LastGood == nil {
		if ap.a.is("json") {
			ap.json(d)
		} else {
			fmt.Fprintln(ap.out, "\n没有找到可以回去的正常状态（DSDock 接管之前的改动没有快照）。请按上面的建议逐个处理。")
		}
		return exitError(7)
	}
	lg := d.LastGood
	ap.printf("\n可以回到 %s 的状态（%s，快照 %s）\n", lg.CreatedAt.Local().Format("01-02 15:04:05"), lg.Reason, lg.ID)
	if !ap.confirm("回到这个状态吗？") {
		if ap.a.is("json") {
			ap.json(d)
		}
		return exitError(7)
	}
	out, err := ap.engine.Restore(ctx, lg.ID, ap.txnOptions())
	if err != nil {
		return err
	}
	if ap.a.is("json") {
		ap.json(map[string]any{"diagnosis": d, "restore": out})
		if out.Applied {
			return nil
		}
		return exitError(3)
	}
	return ap.report(out)
}

func (ap *app) printDiagnosis(d *rescue.Diagnosis) {
	if d.Healthy && len(d.Problems) == 0 {
		how := "配置能正常组装"
		if d.Booted {
			how += "，启动测试通过，所有插件都已激活"
		}
		fmt.Fprintf(ap.out, "✓ 没有发现问题：%s\n", how)
		return
	}
	for _, p := range d.Problems {
		mark := "✗"
		if p.Severity == rescue.Warn {
			mark = "⚠"
		}
		fmt.Fprintf(ap.out, "%s %s\n", mark, p.Message)
		if p.Fix != "" {
			fmt.Fprintf(ap.out, "    → %s\n", p.Fix)
		}
	}
	if !d.Composed {
		fmt.Fprintln(ap.out, "  （配置无法组装，所以没有做启动测试）")
	}
}
