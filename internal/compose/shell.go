package compose

import "strings"

// shellQuote 对一个参数做 POSIX shell 转义：只含安全字符的原样返回，
// 否则用单引号包起来（内部的单引号写成 '"'"'），空串写成 ”。
//
// 用途：健康检查的 `CMD` 形式是参数数组，而 wslc 的 `--health-cmd` 只收一条 shell 命令，
// 拼接时必须保留每个参数的边界。compose-go 只提供拆分（shellwords），不提供拼接，
// 所以这里自己写这十几行。
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			strings.IndexByte("_@%+=:,./-", c) >= 0
		if !ok {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// shellJoin 把参数数组拼成一条 shell 命令。
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}
