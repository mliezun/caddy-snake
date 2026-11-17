package caddysnake

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func setSysProcAttr(cmd *exec.Cmd) {
	// Pdeathsig is not available on Windows; see tieToParent.
}

var (
	workerJobOnce sync.Once
	workerJob     windows.Handle
)

// workerJobHandle returns a job object that Windows closes when this process
// ends, however it ends. Every process in the job is ended with it.
func workerJobHandle() windows.Handle {
	workerJobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil { // #nosec G103 -- fixed-size struct handed to the Windows API
			_ = windows.CloseHandle(h)
			return
		}
		workerJob = h
	})
	return workerJob
}

// tieToParent makes a started worker end when this process does, also when it
// is killed or crashes: the Windows counterpart of Pdeathsig on Linux. Without
// it a killed server leaves workers behind that keep their files locked.
// Best effort: a worker that cannot be tied still runs.
func tieToParent(cmd *exec.Cmd) {
	job := workerJobHandle()
	if job == 0 || cmd.Process == nil {
		return
	}
	pid := uint32(cmd.Process.Pid) // #nosec G115 -- a process id is never negative
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(p) }()
	_ = windows.AssignProcessToJobObject(job, p)
}
