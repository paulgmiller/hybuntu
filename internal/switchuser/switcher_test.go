package switchuser

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestRunSwitchesToInitialVTAfterConfirmedLock(t *testing.T) {
	fake := newFakeSystem()
	fake.greeterFound = false

	switcher := mustSwitcher(t, fake)
	if err := switcher.Run(context.Background(), 4242); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{
		"gdm-version",
		"current:4242",
		"seat-capabilities",
		"active-session",
		"prepare-lock:45",
		"lock:45",
		"wait-lock",
		"still-locked",
		"active-session",
		"find-greeter",
		"switch-vt:1",
		"wait-greeter",
		"close-confirmation",
	}
	if !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("call order = %#v, want %#v", fake.calls, want)
	}
}

func TestRunActivatesExistingGreeter(t *testing.T) {
	fake := newFakeSystem()
	fake.greeterFound = true

	switcher := mustSwitcher(t, fake)
	if err := switcher.Run(context.Background(), 4242); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if containsCall(fake.calls, "switch-vt:1") {
		t.Fatalf("called SwitchToVT with existing greeter: %#v", fake.calls)
	}
	if !containsCall(fake.calls, "activate:greeter") {
		t.Fatalf("did not activate existing greeter: %#v", fake.calls)
	}
}

func TestRunNeverSwitchesWhenLockCannotBeConfirmed(t *testing.T) {
	fake := newFakeSystem()
	fake.confirmation.waitErr = context.DeadlineExceeded

	switcher := mustSwitcher(t, fake)
	err := switcher.Run(context.Background(), 4242)
	if err == nil || !strings.Contains(err.Error(), "refusing to switch away") {
		t.Fatalf("Run() error = %v, want safe lock-confirmation failure", err)
	}
	assertNoSeatChange(t, fake.calls)
}

func TestRunNeverSwitchesWhenLockIsLostBeforeSwitch(t *testing.T) {
	fake := newFakeSystem()
	fake.confirmation.locked = false

	switcher := mustSwitcher(t, fake)
	err := switcher.Run(context.Background(), 4242)
	if err == nil || !strings.Contains(err.Error(), "no longer reported locked") {
		t.Fatalf("Run() error = %v, want lock recheck failure", err)
	}
	assertNoSeatChange(t, fake.calls)
}

func TestRunRecoversToLockedSessionWhenGreeterDoesNotAppear(t *testing.T) {
	fake := newFakeSystem()
	fake.waitGreeterErr = context.DeadlineExceeded

	switcher := mustSwitcher(t, fake)
	err := switcher.Run(context.Background(), 4242)
	if err == nil || !strings.Contains(err.Error(), "returned to locked session") {
		t.Fatalf("Run() error = %v, want recovery error", err)
	}
	if !containsCall(fake.calls, "activate:45") {
		t.Fatalf("recovery did not reactivate original session: %#v", fake.calls)
	}
}

func TestRunRejectsNonGDMCallerBeforeLocking(t *testing.T) {
	fake := newFakeSystem()
	fake.current.Service = "seatd"

	switcher := mustSwitcher(t, fake)
	err := switcher.Run(context.Background(), 4242)
	if err == nil || !strings.Contains(err.Error(), "not GDM") {
		t.Fatalf("Run() error = %v, want non-GDM rejection", err)
	}
	if containsCall(fake.calls, "lock:45") {
		t.Fatalf("locked an invalid caller session: %#v", fake.calls)
	}
}

func TestRunRejectsUnsupportedGDMVersion(t *testing.T) {
	fake := newFakeSystem()
	fake.gdmVersion = "51.0"

	switcher := mustSwitcher(t, fake)
	err := switcher.Run(context.Background(), 4242)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Run() error = %v, want unsupported-version rejection", err)
	}
	if containsCall(fake.calls, "lock:45") {
		t.Fatalf("locked with an unsupported GDM version: %#v", fake.calls)
	}
}

func mustSwitcher(t *testing.T, system System) *Switcher {
	t.Helper()
	switcher, err := New(system, DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return switcher
}

func assertNoSeatChange(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		if strings.HasPrefix(call, "activate:") || strings.HasPrefix(call, "switch-vt:") {
			t.Fatalf("unsafe seat-changing call %q in %#v", call, calls)
		}
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

type fakeSystem struct {
	calls          []string
	current        Session
	activeID       string
	greeter        Session
	greeterFound   bool
	waitGreeterErr error
	recoveryErr    error
	confirmation   *fakeLockConfirmation
	gdmVersion     string
}

func newFakeSystem() *fakeSystem {
	fake := &fakeSystem{
		current: Session{
			ID:       "45",
			Path:     "/org/freedesktop/login1/session/_345",
			SeatID:   "seat0",
			SeatPath: "/org/freedesktop/login1/seat/seat0",
			Class:    "user",
			Service:  "gdm-password",
			Type:     "wayland",
			State:    "active",
			TTY:      "tty2",
			VT:       2,
			Active:   true,
		},
		activeID: "45",
		greeter: Session{
			ID:      "greeter",
			Class:   "greeter",
			Service: "gdm-launch-environment",
			State:   "active",
			TTY:     "tty1",
			VT:      1,
			Active:  true,
		},
	}
	fake.confirmation = &fakeLockConfirmation{parent: fake, locked: true}
	fake.gdmVersion = "50.1"
	return fake
}

func (f *fakeSystem) DisplayManagerVersion(context.Context) (string, error) {
	f.calls = append(f.calls, "gdm-version")
	return f.gdmVersion, nil
}

func (f *fakeSystem) CurrentSession(_ context.Context, pid uint32) (Session, error) {
	f.calls = append(f.calls, "current:"+itoa(pid))
	return f.current, nil
}

func (f *fakeSystem) SeatCapabilities(context.Context, string) (SeatCapabilities, error) {
	f.calls = append(f.calls, "seat-capabilities")
	return SeatCapabilities{CanTTY: true, CanGraphical: true}, nil
}

func (f *fakeSystem) ActiveSession(context.Context, string) (string, error) {
	f.calls = append(f.calls, "active-session")
	return f.activeID, nil
}

func (f *fakeSystem) FindGreeter(context.Context, string) (Session, bool, error) {
	f.calls = append(f.calls, "find-greeter")
	return f.greeter, f.greeterFound, nil
}

func (f *fakeSystem) PrepareLockConfirmation(_ context.Context, session Session) (LockConfirmation, error) {
	f.calls = append(f.calls, "prepare-lock:"+session.ID)
	return f.confirmation, nil
}

func (f *fakeSystem) LockSession(_ context.Context, id string) error {
	f.calls = append(f.calls, "lock:"+id)
	return nil
}

func (f *fakeSystem) ActivateSession(_ context.Context, sessionID, _ string) error {
	f.calls = append(f.calls, "activate:"+sessionID)
	if sessionID == f.current.ID {
		return f.recoveryErr
	}
	return nil
}

func (f *fakeSystem) SwitchToVT(_ context.Context, _ string, vt uint32) error {
	f.calls = append(f.calls, "switch-vt:"+itoa(vt))
	return nil
}

func (f *fakeSystem) WaitGreeterActive(context.Context, string) (Session, error) {
	f.calls = append(f.calls, "wait-greeter")
	return f.greeter, f.waitGreeterErr
}

type fakeLockConfirmation struct {
	parent  *fakeSystem
	locked  bool
	waitErr error
}

func (f *fakeLockConfirmation) Wait(context.Context) (string, error) {
	f.parent.calls = append(f.parent.calls, "wait-lock")
	return "fake", f.waitErr
}

func (f *fakeLockConfirmation) StillLocked(context.Context) (bool, error) {
	f.parent.calls = append(f.parent.calls, "still-locked")
	return f.locked, nil
}

func (f *fakeLockConfirmation) Close() {
	f.parent.calls = append(f.parent.calls, "close-confirmation")
}

func itoa(value uint32) string {
	if value == 0 {
		return "0"
	}
	var digits [10]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
