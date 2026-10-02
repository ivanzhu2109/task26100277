package masker_test

import (
	"fmt"
	"masker"
	"os"
	"path/filepath"
)

// Example 演示完整调用流程：加载词典、按消息创建处理过程、
// 分段提交、结束消息，调用方负责保存各次返回的结果。
func Example() {
	dir, _ := os.MkdirTemp("", "dict")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "words.txt")
	os.WriteFile(path, []byte("aba\nbab\n🙂🙃\n"), 0o644)

	dict, err := masker.LoadDictionary(path)
	if err != nil {
		panic(err)
	}

	msg := dict.NewMessage()
	var saved []byte                                  // 调用方保存：各次结果只追加
	saved = append(saved, msg.Feed([]byte("ab"))...)  // 片段 1
	saved = append(saved, msg.Feed([]byte("ab"))...)  // 片段 2（跨片段命中 aba/bab）
	saved = append(saved, msg.Feed([]byte("a🙂🙃"))...) // 片段 3
	saved = append(saved, msg.End()...)               // 结束，取回全部剩余
	fmt.Println(string(saved))
	// Output: *******
}
