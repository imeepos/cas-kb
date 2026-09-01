package main

import "fmt"

// flags 是极简 "--key value" 参数解析结果。
type flags struct {
	options map[string]string
	pos     []string
}

// parseFlags 解析参数;valFlags 是需要取值的旗标集合,其余视为布尔旗标。
func parseFlags(args []string, valFlags map[string]bool) (*flags, error) {
	f := &flags{options: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 1 && a[0] == '-' {
			if valFlags[a] {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("选项 %s 缺少值", a)
				}
				f.options[a] = args[i+1]
				i++
			} else {
				f.options[a] = "true"
			}
			continue
		}
		f.pos = append(f.pos, a)
	}
	return f, nil
}

// has 报告布尔旗标是否出现。
func (f *flags) has(name string) bool { _, ok := f.options[name]; return ok }

// get 返回旗标值;缺省返回 def。
func (f *flags) get(name, def string) string {
	if v, ok := f.options[name]; ok {
		return v
	}
	return def
}
