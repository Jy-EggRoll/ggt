package cmd

import "testing"

// TestDecideSyncAction 用表驱动覆盖 sync 命令的全部决策分支。
//
// 抽成纯函数之前，这段判定写在 syncRepo 里，与 git 调用、输出着色混在一起，
// 只能靠人手动造四个不同的仓库状态去验证，于是长期是 0% 覆盖。
// 现在它不接触 git 也不产生输出，"本地/远程/共同祖先"三者的相等关系正好穷尽四种结果。
func TestDecideSyncAction(t *testing.T) {
	// 三个互不相同的假 commit hash。特意不用 a/b/c 这种单字符，
	// 以免有人误以为它们在比较"顺序"或"大小"——这里只比较相等关系
	const (
		older  = "1111111"
		middle = "2222222"
		newer  = "3333333"
	)

	cases := []struct {
		name   string
		local  string
		remote string
		base   string
		want   syncAction
	}{
		{
			name:   "三者相同：已是最新",
			local:  middle,
			remote: middle,
			base:   middle,
			want:   syncUpToDate,
		},
		{
			name:   "本地与远程相同：已是最新（判定优先于共同祖先）",
			local:  middle,
			remote: middle,
			base:   older,
			want:   syncUpToDate,
		},
		{
			name:   "本地等于共同祖先：线性落后，可快进",
			local:  older,
			remote: newer,
			base:   older,
			want:   syncFastForward,
		},
		{
			name:   "远程等于共同祖先：本地领先，只能手动推送",
			local:  newer,
			remote: older,
			base:   older,
			want:   syncAhead,
		},
		{
			name:   "三者互不相等：历史分叉，必须人工处理",
			local:  older,
			remote: newer,
			base:   middle,
			want:   syncDiverged,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideSyncAction(c.local, c.remote, c.base); got != c.want {
				t.Errorf("decideSyncAction(%q, %q, %q) = %v, want %v",
					c.local, c.remote, c.base, got, c.want)
			}
		})
	}
}

// TestDecideSyncActionNeverAutoMerges 是一条语义防线，而不是行为测试：
// 只有"本地严格落后"这一种情形允许 ggt 自动动仓库，其余一律交回用户。
//
// 之所以单独立一条：判断顺序一旦被改动（例如把 remote == base 提到 local == base 之前），
// 分叉或领先的仓库就可能被误判成可快进而被自动 pull，那是会丢改动的操作。
func TestDecideSyncActionNeverAutoMerges(t *testing.T) {
	const (
		a = "aaaaaaa"
		b = "bbbbbbb"
		c = "ccccccc"
	)

	// 穷举三个 hash 两两相等/不相等组合中所有"非本地落后"的情形
	for _, tc := range [][3]string{
		{a, a, a}, // 一致
		{a, a, b}, // 本地与远程一致
		{b, a, a}, // 本地领先
		{a, b, c}, // 分叉
	} {
		if got := decideSyncAction(tc[0], tc[1], tc[2]); got == syncFastForward {
			t.Errorf("decideSyncAction(%q, %q, %q) 误判为可快进", tc[0], tc[1], tc[2])
		}
	}
}
