// platctl — Platfarm 平台工具：new / sync / check / list。
// 纪律执行器：网关与服务编排配置只能由本工具从 service.yaml 生成。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "platctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	root, err := findRoot()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "new":
		id, lang := "", "py"
		for i := 1; i < len(args); i++ {
			if args[i] == "--lang" && i+1 < len(args) {
				lang = args[i+1]
				i++
				continue
			}
			id = args[i]
		}
		if id == "" {
			return fmt.Errorf("用法: platctl new <svc-id> [--lang py]")
		}
		return runNew(root, id, lang)
	case "sync":
		return runSync(root)
	case "check":
		e2e := len(args) > 1 && args[1] == "--e2e"
		return runCheck(root, e2e)
	case "list":
		return runList(root)
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("用法: platctl install <插件目录>")
		}
		return runInstall(root, args[1])
	case "enable", "disable":
		if len(args) < 2 {
			return fmt.Errorf("用法: platctl %s <svc-id>", args[0])
		}
		return runToggle(root, args[1], args[0] == "enable")
	case "uninstall":
		if len(args) < 2 {
			return fmt.Errorf("用法: platctl uninstall <svc-id> [--purge]")
		}
		purge := len(args) > 2 && args[2] == "--purge"
		return runUninstall(root, args[1], purge)
	case "upgrade":
		if len(args) < 3 {
			return fmt.Errorf("用法: platctl upgrade <svc-id> <新插件目录>")
		}
		if err := runUninstall(root, args[1], false); err != nil {
			return err
		}
		return runInstall(root, args[2])
	default:
		return usageError()
	}
}

func usageError() error {
	return fmt.Errorf(`用法:
  platctl new <svc-id> [--lang py]    从模板生成第一方服务骨架
  platctl sync                         清单 → kong.yml + compose 片段（含密钥自举）
  platctl check [--e2e]                清单校验（--e2e 附加运行容器内契约测试）
  platctl list                         平台服务总览
  platctl install <插件目录>            安装第三方插件（开库开号+发凭据+契约测试闸门）
  platctl enable|disable <svc-id>      启停服务/插件（摘挂路由）
  platctl uninstall <svc-id> [--purge] 卸载（--purge 连数据一起删）
  platctl upgrade <svc-id> <新目录>     升级（卸载保数据 + 重装）`)
}

// findRoot 从当前目录向上找平台根（以 gateway/ 目录 + docker-compose.yml 为标志）。
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(dir + string(os.PathSeparator) + "gateway"); err == nil {
			if _, err := os.Stat(dir + string(os.PathSeparator) + "docker-compose.yml"); err == nil {
				return dir, nil
			}
		}
		parent := parentDir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到平台根目录（需含 gateway/ 与 docker-compose.yml）")
		}
		dir = parent
	}
}

func parentDir(dir string) string {
	trimmed := strings.TrimRight(dir, string(os.PathSeparator))
	idx := strings.LastIndex(trimmed, string(os.PathSeparator))
	if idx < 0 {
		return dir
	}
	if parent := trimmed[:idx]; parent != "" {
		return parent
	}
	return dir
}

func runList(root string) error {
	manifests, err := loadManifests(root)
	if err != nil {
		return err
	}
	fmt.Printf("%-20s %-9s %-14s %-12s %-8s %-8s %s\n", "ID", "VERSION", "MOUNT", "TRUST", "AUTH", "STATE", "RATE/MIN")
	for _, m := range manifests {
		auth := "public"
		if m.Auth.Required {
			auth = "jwt"
		}
		trust := m.Trust
		if trust == "" {
			trust = "first-party"
		}
		state := "enabled"
		if !m.IsEnabled() {
			state = "disabled"
		}
		fmt.Printf("%-20s %-9s %-14s %-12s %-8s %-8s %d\n", m.ID, m.Version, m.Mount.Path, trust, auth, state, m.Limits.RatePerMinute)
	}
	return nil
}

func composeExec(root string, cmdArgs ...string) error {
	args := append([]string{"compose", "exec", "-T"}, cmdArgs...)
	cmd := exec.Command("docker", args...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
