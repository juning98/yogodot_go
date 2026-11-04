package theme

import "github.com/wednesque/yogod/internal/frame"

// 形状掩码：这些不是装饰，是各自主题的标志物。
//
// 6 行 × 16 列，和键盘的物理格子一一对应；'X' = 命中，其余字符（含空格）
// 都算"没命中"。数据里个别行末尾留着空格 —— 那是原稿的列宽对齐，
// 不影响结果（空格本来就不是 'X'），照抄不动。

// MBolt 是闪电：皮卡丘。
var MBolt = [frame.KBRows]string{
	".........XXXX...",
	".......XXXX.....",
	".....XXXXXXX....",
	"...XXXXXXX......",
	".....XXX........",
	"...XXX..........",
}

// MCrack 是熔岩裂缝：黑色岩壳下发光的缝。
var MCrack = [frame.KBRows]string{
	"....X........X..",
	"....X.......XX..",
	".....XXX...XX...",
	"XXXXXX..XXXX....",
	"......XX....XXXX",
	".......X........",
}

// MEyes 是熊猫的两块黑眼圈。
var MEyes = [frame.KBRows]string{
	"................",
	"...XXX....XXX...",
	"...XXX....XXX...",
	"...XXX....XXX...",
	"................",
	"................",
}

// MTails 是初音的双马尾：两侧到底的宽带。
var MTails = [frame.KBRows]string{
	"XXX..........XXX",
	"XXX..........XX ",
	"XXX..........XXX",
	"XXX..........XX ",
	"XXX..........XX ",
	"XXX..........XXX",
}

// ECG 是心电图波形（心跳主题的标志物）。
var ECG = [frame.KBRows]string{
	"................",
	"................",
	"......X.........",
	"XXXXXX.X.XXXXXXX",
	".......X........",
	"................",
}
