// Command joblimit runs a Windows command and its descendants in a hard 4 GiB Job.
// This executable is cross-compiled under the Linux cgroup before Windows CI.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

type basicLimits struct {
	ProcessTime, JobTime                 int64
	Flags                                uint32
	MinimumWorkingSet, MaximumWorkingSet uintptr
	ActiveProcesses                      uint32
	Affinity                             uintptr
	Priority, Scheduling                 uint32
}
type extendedLimits struct {
	Basic                                                      basicLimits
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func run(args []string) (int, error) {
	if len(args) == 0 {
		return 1, errors.New("usage: joblimit.exe command [args...]")
	}
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return 1, errors.New("64-bit Windows is required")
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	job, _, err := kernel.NewProc("CreateJobObjectW").Call(0, 0)
	if job == 0 {
		return 1, fmt.Errorf("CreateJobObject: %w", err)
	}
	defer syscall.CloseHandle(syscall.Handle(job))
	const memoryFlag = 0x200
	limits := extendedLimits{Basic: basicLimits{Flags: memoryFlag | 0x2000}, JobMemory: 4 << 30}
	ok, _, err := kernel.NewProc("SetInformationJobObject").Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		return 1, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	var actual extendedLimits
	ok, _, err = kernel.NewProc("QueryInformationJobObject").Call(job, 9, uintptr(unsafe.Pointer(&actual)), unsafe.Sizeof(actual), 0)
	if ok == 0 || actual.JobMemory != 4<<30 || actual.Basic.Flags&memoryFlag == 0 {
		return 1, errors.New("hard Job memory limit could not be verified")
	}
	for key, value := range map[string]string{"GOMAXPROCS": "1", "GOMEMLIMIT": "2GiB", "GOFLAGS": "-p=1", "GOTOOLCHAIN": "local", "CGO_ENABLED": "0"} {
		if err := os.Setenv(key, value); err != nil {
			return 1, err
		}
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		return 1, err
	}
	application, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 1, err
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = syscall.EscapeArg(arg)
	}
	line, err := syscall.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return 1, err
	}
	startup := syscall.StartupInfo{Flags: syscall.STARTF_USESTDHANDLES, StdInput: syscall.Handle(os.Stdin.Fd()), StdOutput: syscall.Handle(os.Stdout.Fd()), StdErr: syscall.Handle(os.Stderr.Fd())}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	for _, handle := range []syscall.Handle{startup.StdInput, startup.StdOutput, startup.StdErr} {
		if err := syscall.SetHandleInformation(handle, 1, 1); err != nil {
			return 1, fmt.Errorf("inherit standard handle: %w", err)
		}
	}
	var child syscall.ProcessInformation
	// Suspend before user code can run; assignment must succeed before ResumeThread.
	if err := syscall.CreateProcess(application, line, nil, nil, true, 0x4, nil, nil, &startup, &child); err != nil {
		return 1, err
	}
	defer syscall.CloseHandle(child.Process)
	defer syscall.CloseHandle(child.Thread)
	finished := false
	defer func() {
		if !finished {
			_ = syscall.TerminateProcess(child.Process, 1)
		}
	}()
	ok, _, err = kernel.NewProc("AssignProcessToJobObject").Call(job, uintptr(child.Process))
	if ok == 0 {
		return 1, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	var included uint32
	ok, _, err = kernel.NewProc("IsProcessInJob").Call(uintptr(child.Process), job, uintptr(unsafe.Pointer(&included)))
	if ok == 0 || included == 0 {
		return 1, errors.New("child Job assignment could not be verified")
	}
	fmt.Fprintln(os.Stderr, "Verified JobMemoryLimit=4294967296; one build worker.")
	resumed, _, err := kernel.NewProc("ResumeThread").Call(uintptr(child.Thread))
	if uint32(resumed) == 0xffffffff {
		return 1, fmt.Errorf("ResumeThread: %w", err)
	}
	if _, err := syscall.WaitForSingleObject(child.Process, syscall.INFINITE); err != nil {
		return 1, err
	}
	var code uint32
	if err := syscall.GetExitCodeProcess(child.Process, &code); err != nil {
		return 1, err
	}
	finished = true
	return int(code), nil
}
func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}
