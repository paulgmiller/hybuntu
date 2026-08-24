package switchuser

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestDecodeSession(t *testing.T) {
	path := dbus.ObjectPath("/org/freedesktop/login1/session/_345")
	props := map[string]dbus.Variant{
		"Id":         dbus.MakeVariant("45"),
		"Seat":       dbus.MakeVariant(seatRef{"seat0", "/org/freedesktop/login1/seat/seat0"}),
		"Class":      dbus.MakeVariant("user"),
		"Service":    dbus.MakeVariant("gdm-password"),
		"Type":       dbus.MakeVariant("wayland"),
		"State":      dbus.MakeVariant("active"),
		"TTY":        dbus.MakeVariant("tty2"),
		"VTNr":       dbus.MakeVariant(uint32(2)),
		"Active":     dbus.MakeVariant(true),
		"LockedHint": dbus.MakeVariant(false),
		"Remote":     dbus.MakeVariant(false),
	}

	session, err := decodeSession(path, props)
	if err != nil {
		t.Fatalf("decodeSession() error = %v", err)
	}
	if session.ID != "45" || session.SeatID != "seat0" || session.VT != 2 || !session.Active {
		t.Fatalf("decodeSession() = %#v", session)
	}
}

func TestDecodeSessionRequiresLockedHint(t *testing.T) {
	props := map[string]dbus.Variant{}
	_, err := decodeSession("/org/freedesktop/login1/session/_345", props)
	if err == nil || err.Error() != "D-Bus property \"Id\" is missing" {
		t.Fatalf("decodeSession() error = %v", err)
	}
}

func TestDBusErrorNamedAcceptsPointerAndValue(t *testing.T) {
	const name = "org.freedesktop.login1.NoSessionForPID"
	if !dbusErrorNamed(dbus.NewError(name, nil), name) {
		t.Fatal("dbusErrorNamed() did not match *dbus.Error")
	}
	if !dbusErrorNamed(dbus.Error{Name: name}, name) {
		t.Fatal("dbusErrorNamed() did not match dbus.Error value")
	}
}

func TestLiveReadOnlySystemInterfaces(t *testing.T) {
	if os.Getenv("HYPR_SWITCH_USER_LIVE_TEST") != "1" {
		t.Skip("set HYPR_SWITCH_USER_LIVE_TEST=1 to probe the current session read-only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	system, err := ConnectSystemBus(ctx, 3*time.Second)
	if err != nil {
		t.Fatalf("ConnectSystemBus() error = %v", err)
	}
	defer system.Close()

	if _, err := system.DisplayManagerVersion(ctx); err != nil {
		t.Fatalf("DisplayManagerVersion() error = %v", err)
	}
	session, err := system.CurrentSession(ctx, uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("CurrentSession() error = %v", err)
	}
	if _, err := system.SeatCapabilities(ctx, session.SeatPath); err != nil {
		t.Fatalf("SeatCapabilities() error = %v", err)
	}
	if _, err := system.ActiveSession(ctx, session.SeatPath); err != nil {
		t.Fatalf("ActiveSession() error = %v", err)
	}
	if _, _, err := system.FindGreeter(ctx, session.SeatPath); err != nil {
		t.Fatalf("FindGreeter() error = %v", err)
	}
	confirmation, err := system.PrepareLockConfirmation(ctx, session)
	if err != nil {
		t.Fatalf("PrepareLockConfirmation() error = %v", err)
	}
	confirmation.Close()
}
