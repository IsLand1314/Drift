package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadDotEnv 读取一个受限的 KEY=VALUE 文件。
//
// 它只处理空行、整行注释和简单单双引号，不做变量展开；缺少文件表示
// 用户没有使用 .env，应返回空配置而不是启动错误。错误只包含行号，避免回显密钥。
func LoadDotEnv(path string) (map[string]string, error) {
	values := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, fmt.Errorf("read dotenv: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 只按第一个等号拆分，允许值本身继续包含等号。
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid dotenv entry at line %d", lineNumber)
		}
		value = strings.TrimSpace(value)
		// 示例文件常用单双引号包住空值或 URL，这里只去掉成对外引号。
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') {
			quote := value[0]
			if value[len(value)-1] != quote {
				return nil, fmt.Errorf("invalid dotenv entry at line %d", lineNumber)
			}
			value = value[1 : len(value)-1]
		} else if len(value) == 1 && (value[0] == '\'' || value[0] == '"') {
			return nil, fmt.Errorf("invalid dotenv entry at line %d", lineNumber)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read dotenv at line %d: %w", lineNumber+1, err)
	}
	return values, nil
}

// MergeLookup 实现单个配置项的来源优先级：非空进程环境变量 > .env。
// 命令行 flag 在 app.Run 中由 flag.Parse 后再覆盖默认值，因此优先级最高。
func MergeLookup(dotenv map[string]string, getenv func(string) string, key string) string {
	if value := getenv(key); value != "" {
		return value
	}
	return dotenv[key]
}
