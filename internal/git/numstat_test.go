package git

import "testing"

// TestParseNumstat 用实测到的字节形态验证解析：普通、二进制、重命名三种记录。
// 三份样本都是从真仓库上原样抄下来的（见 CommitFiles 的注释）
func TestParseNumstat(t *testing.T) {
	t.Run("普通与二进制混排", func(t *testing.T) {
		out := "0\t1\ta.txt\x00-\t-\tbin.dat\x002\t0\trenamed.txt\x00"
		files := parseNumstat(out)
		if len(files) != 3 {
			t.Fatalf("应解析出 3 个文件，实得 %d：%+v", len(files), files)
		}
		if files[0].Path != "a.txt" || files[0].Adds != 0 || files[0].Dels != 1 || files[0].Binary {
			t.Errorf("第一条不对：%+v", files[0])
		}
		if !files[1].Binary || files[1].Path != "bin.dat" {
			t.Errorf("二进制那条应标成 Binary 且不带行数：%+v", files[1])
		}
		if files[2].Adds != 2 || files[2].Path != "renamed.txt" {
			t.Errorf("第三条不对：%+v", files[2])
		}
	})

	t.Run("重命名", func(t *testing.T) {
		// 形态：第三个字段为空，紧跟旧路径与新路径
		files := parseNumstat("1\t0\t\x00big.txt\x00moved.txt\x00")
		if len(files) != 1 {
			t.Fatalf("应解析出 1 个文件，实得 %d：%+v", len(files), files)
		}
		if files[0].Path != "moved.txt" || files[0].OrigPath != "big.txt" {
			t.Errorf("重命名应给出新旧两个路径：%+v", files[0])
		}
		if files[0].Adds != 1 || files[0].Dels != 0 {
			t.Errorf("重命名的行数不对：%+v", files[0])
		}
	})

	t.Run("纯重命名与多文件", func(t *testing.T) {
		out := "0\t0\t\x00moved.txt\x00pure.txt\x001\t1\tx.txt\x00"
		files := parseNumstat(out)
		if len(files) != 2 {
			t.Fatalf("应解析出 2 个文件，实得 %d：%+v", len(files), files)
		}
		if files[0].OrigPath != "moved.txt" || files[0].Path != "pure.txt" || files[0].Adds != 0 {
			t.Errorf("纯重命名不对：%+v", files[0])
		}
		if files[1].Path != "x.txt" || files[1].Adds != 1 || files[1].Dels != 1 {
			t.Errorf("紧跟其后的普通记录被误读了：%+v", files[1])
		}
	})

	t.Run("空输出与截断记录", func(t *testing.T) {
		if files := parseNumstat(""); len(files) != 0 {
			t.Errorf("空输出应给空列表：%+v", files)
		}
		// 重命名只给到旧路径就断了：宁可少一条，也不要拿一个不存在的路径当新路径
		if files := parseNumstat("1\t0\t\x00only-old.txt\x00"); len(files) != 0 {
			t.Errorf("截断的重命名记录应被丢弃：%+v", files)
		}
	})
}
