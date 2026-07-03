//go:build windows

package proc

import (
	"os/exec"
	"sort"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TreeTracker records a process tree while a command is running. Windows Job
// Objects should own normal children, but Git Bash/MSYS launch chains can briefly
// expose grandchildren before or outside taskkill's live tree walk. Recording
// descendants gives cancellation a second chance to terminate those escapees.
type TreeTracker struct {
	root uint32
	done chan struct{}
	once sync.Once

	mu   sync.Mutex
	pids map[uint32]struct{}
}

func TrackTree(cmd *exec.Cmd) *TreeTracker {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	t := &TreeTracker{
		root: uint32(cmd.Process.Pid),
		done: make(chan struct{}),
		pids: map[uint32]struct{}{},
	}
	t.record()
	go t.loop()
	return t
}

func (t *TreeTracker) Stop() {
	if t == nil {
		return
	}
	t.once.Do(func() { close(t.done) })
}

func (t *TreeTracker) Kill() {
	if t == nil {
		return
	}
	t.record()
	pids := t.snapshot()
	for _, pid := range pids {
		if pid != t.root {
			terminatePID(pid)
		}
	}
	terminatePID(t.root)
}

func (t *TreeTracker) loop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.record()
		case <-t.done:
			return
		}
	}
}

func (t *TreeTracker) record() {
	if t == nil || t.root == 0 {
		return
	}
	pids := descendantPIDs(t.root)
	t.mu.Lock()
	t.pids[t.root] = struct{}{}
	for _, pid := range pids {
		t.pids[pid] = struct{}{}
	}
	t.mu.Unlock()
}

func (t *TreeTracker) snapshot() []uint32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]uint32, 0, len(t.pids))
	for pid := range t.pids {
		out = append(out, pid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func descendantPIDs(root uint32) []uint32 {
	if root == 0 {
		return nil
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	children := map[uint32][]uint32{}
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err := windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		children[pe.ParentProcessID] = append(children[pe.ParentProcessID], pe.ProcessID)
	}

	var out []uint32
	var walk func(uint32)
	walk = func(pid uint32) {
		for _, child := range children[pid] {
			out = append(out, child)
			walk(child)
		}
	}
	walk(root)
	return out
}

func terminatePID(pid uint32) {
	if pid == 0 {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(h) }()
	_ = windows.TerminateProcess(h, 1)
}
