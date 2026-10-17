package coreproc

import (
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CREATE_NO_WINDOW: у ядра нет своего окна и нет консоли оболочки, поэтому
// Ctrl+C в консоли оболочки до него не доходит.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

func lockThread() func() { return func() {} }

// job — задание Windows с ядром внутри.
type job windows.Handle

func (j job) Close() error { return windows.CloseHandle(windows.Handle(j)) }

// guardChild кладёт ядро в задание с KILL_ON_JOB_CLOSE. Описатель задания
// есть только у оболочки и не наследуется, так что система закроет его,
// когда оболочка выйдет или упадёт, а с ним убьёт и ядро.
//
// Между запуском и заданием ядро успевает поработать без присмотра — это
// миг, и своих процессов ядро не порождает.
func guardChild(p *os.Process) (io.Closer, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("задание для ядра: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("задание для ядра: %w", err)
	}
	// Описатель ядра держит os.Process, так что его pid не займёт другой процесс.
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("задание для ядра: %w", err)
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(h, ph); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("задание для ядра: %w", err)
	}
	return job(h), nil
}
