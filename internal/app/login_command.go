package app

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/IsLand1314/Drift/internal/config"
	term "github.com/charmbracelet/x/term"
)

func runLoginCommand(args []string, in io.Reader, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("drift login", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "Provider profile")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || strings.TrimSpace(*provider) == "" {
		if err == nil {
			fmt.Fprintln(stderr, "用法：drift login --provider <name>")
		}
		return 2
	}
	workspace, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取启动目录")
		return 1
	}
	configDir := filepath.Join(workspace, ".drift")
	userConfig, err := config.LoadUserConfig(configDir)
	if err != nil || !userConfig.ConfigPresent {
		fmt.Fprintln(stderr, "错误：未找到 Provider 配置")
		return 2
	}
	profile, ok := userConfig.Provider(strings.TrimSpace(*provider))
	if !ok {
		fmt.Fprintln(stderr, "错误：未找到 Provider profile：", strings.TrimSpace(*provider))
		return 2
	}
	if profile.Protocol == "codex" {
		fmt.Fprintln(stderr, "错误：Codex Provider 不支持 API Key 登录")
		return 2
	}
	key, err := readLoginAPIKey(in, out)
	if err != nil {
		fmt.Fprintln(stderr, "错误：读取 API Key 失败")
		return 1
	}
	if err := config.SaveAPIKey(configDir, profile.Name, key); err != nil {
		fmt.Fprintln(stderr, "错误：保存 API Key 失败")
		return 1
	}
	fmt.Fprintln(out, "API Key 已保存")
	return 0
}

func readLoginAPIKey(in io.Reader, out io.Writer) (string, error) {
	fmt.Fprint(out, "API Key: ")
	if file, ok := in.(*os.File); ok && isTTY(file) {
		value, err := term.ReadPassword(file.Fd())
		fmt.Fprintln(out)
		return strings.TrimSpace(string(value)), err
	}
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return strings.TrimSpace(scanner.Text()), nil
}
