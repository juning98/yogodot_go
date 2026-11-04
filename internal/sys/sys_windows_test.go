//go:build windows

package sys

import (
	"fmt"
	"testing"
	"time"
)

/* ══════════ 单实例锁 ══════════

   这是"两个实例抢同一把键盘"那条 P1 的直接防线。
   它坏掉不会让任何东西编译不过，只会让用户某天发现键盘在抽搐。 */

func TestSingleInstanceBlocks(t *testing.T) {
	name := fmt.Sprintf("yogod-test-%d", time.Now().UnixNano())

	first, err := AcquireSingleInstance(name)
	if err != nil {
		t.Fatalf("第一次加锁失败：%v", err)
	}
	defer first.Close()
	if !first.Acquired() {
		t.Fatal("第一次加锁应成功")
	}

	second, err := AcquireSingleInstance(name)
	if err != nil {
		t.Fatalf("第二次加锁返回错误：%v", err)
	}
	defer second.Close()
	if second.Acquired() {
		t.Fatal("同名互斥体第二次加锁不该成功 —— 单实例保护失效")
	}
}

func TestSingleInstanceReleasedAfterClose(t *testing.T) {
	name := fmt.Sprintf("yogod-test-%d", time.Now().UnixNano())

	first, err := AcquireSingleInstance(name)
	if err != nil {
		t.Fatalf("加锁失败：%v", err)
	}
	first.Close()

	again, err := AcquireSingleInstance(name)
	if err != nil {
		t.Fatalf("释放后重新加锁失败：%v", err)
	}
	defer again.Close()
	if !again.Acquired() {
		t.Fatal("上一个实例关掉后应该能重新拿到锁")
	}
}

// TestInstanceCloseIsIdempotent：defer 和显式 Close 撞在一起是常态，不能 panic。
func TestInstanceCloseIsIdempotent(t *testing.T) {
	inst, err := AcquireSingleInstance(fmt.Sprintf("yogod-test-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("加锁失败：%v", err)
	}
	inst.Close()
	inst.Close()
	inst.Close()

	var nilInst *Instance
	nilInst.Close() // nil 也得安全
	if nilInst.Acquired() {
		t.Error("nil 实例不该报告已持有锁")
	}
}

func TestEmptyNameFails(t *testing.T) {
	// `Global\` 后面什么都没有 —— 只要不 panic、错误能带回来就行
	if _, err := AcquireSingleInstance(""); err != nil {
		t.Logf("空名字返回错误（可以接受）：%v", err)
	}
}

/* ══════════ Job Object ══════════

   注意：**绝不**在这里调 Assign()。把测试进程挂进
   KILL_ON_JOB_CLOSE 的 job，Close 的一瞬间 go test 自己就被杀了。 */

func TestJobLifecycle(t *testing.T) {
	j, err := NewJob()
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}
	if j == nil {
		t.Fatal("NewJob 返回 nil 却没报错")
	}
	j.Close()
	j.Close() // 重复关

	var nilJob *Job
	nilJob.Close()
	if err := nilJob.Assign(); err != nil {
		t.Errorf("nil job 的 Assign 应为空操作，实际 %v", err)
	}
	if err := nilJob.AssignPID(nil); err != nil {
		t.Errorf("nil job 的 AssignPID 应为空操作，实际 %v", err)
	}
	// 已关闭的 job 上再挂进程也必须是空操作，不能拿 0 句柄去调 Win32
	if err := j.Assign(); err != nil {
		t.Errorf("已关闭 job 的 Assign 应为空操作，实际 %v", err)
	}
}
