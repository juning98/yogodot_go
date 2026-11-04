//go:build windows

package sys

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	procCreateJobObjectW         = modk32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = modk32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = modk32.NewProc("AssignProcessToJobObject")
	procGetCurrentProcess        = modk32.NewProc("GetCurrentProcess")
)

// JobObjectExtendedLimitInformation 是 JobObjectInformationClass 的取值。
const jobObjectExtendedLimitInformation = 9

// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE：句柄全关时内核杀掉 job 里所有进程。
const jobObjectLimitKillOnJobClose = 0x2000

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobObjectExtendedLimitInformation_ struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// Job 是一个 Windows Job Object。
//
// 存在的理由：Windows 不级联杀子进程。旧版为此用 PowerShell 去
// `Get-CimInstance Win32_Process` 匹配命令行再 Stop-Process ——
// 又慢又脆（命令行里带空格、路径变了就失手）。
// 挂进 Job Object 之后，父进程一死，内核负责清场。
type Job struct {
	handle syscall.Handle
}

// NewJob 创建一个"随宿主退出自动清理子进程"的 job。
func NewJob() (*Job, error) {
	h, _, err := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return nil, errors.Join(errors.New("sys: CreateJobObject 失败"), err)
	}
	j := &Job{handle: syscall.Handle(h)}

	var info jobObjectExtendedLimitInformation_
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r, _, err := procSetInformationJobObject.Call(
		uintptr(j.handle), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		syscall.CloseHandle(j.handle)
		return nil, errors.Join(errors.New("sys: SetInformationJobObject 失败"), err)
	}
	return j, nil
}

// Assign 把当前进程挂进 job。
//
// 挂上之后，本进程及其所有子孙进程都会随 job 句柄关闭而被杀 ——
// 包括 ssh 这类会残留的子进程。
func (j *Job) Assign() error {
	if j == nil || j.handle == 0 {
		return nil
	}
	p, _, _ := procGetCurrentProcess.Call()
	r, _, err := procAssignProcessToJobObject.Call(uintptr(j.handle), p)
	if r == 0 {
		return errors.Join(errors.New("sys: AssignProcessToJobObject 失败"), err)
	}
	return nil
}

// AssignPID 把一个已启动的子进程挂进 job。
func (j *Job) AssignPID(p *os.Process) error {
	if j == nil || j.handle == 0 || p == nil {
		return nil
	}
	h, err := syscall.OpenProcess(0x0100|0x0001 /* PROCESS_SET_QUOTA | PROCESS_TERMINATE */, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	r, _, callErr := procAssignProcessToJobObject.Call(uintptr(j.handle), uintptr(h))
	if r == 0 {
		return errors.Join(errors.New("sys: AssignProcessToJobObject 失败"), callErr)
	}
	return nil
}

// Close 关闭 job 句柄（会连带杀掉里面所有进程）。
func (j *Job) Close() {
	if j == nil || j.handle == 0 {
		return
	}
	syscall.CloseHandle(j.handle)
	j.handle = 0
}
