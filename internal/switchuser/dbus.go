package switchuser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	loginName                        = "org.freedesktop.login1"
	loginManager                     = "org.freedesktop.login1.Manager"
	loginSession                     = "org.freedesktop.login1.Session"
	loginSeat                        = "org.freedesktop.login1.Seat"
	loginManagerPath dbus.ObjectPath = "/org/freedesktop/login1"

	gdmName                        = "org.gnome.DisplayManager"
	gdmManager                     = "org.gnome.DisplayManager.Manager"
	gdmManagerPath dbus.ObjectPath = "/org/gnome/DisplayManager/Manager"

	propertiesInterface = "org.freedesktop.DBus.Properties"
)

type DBusSystem struct {
	conn        *dbus.Conn
	callTimeout time.Duration
}

func ConnectSystemBus(ctx context.Context, callTimeout time.Duration) (*DBusSystem, error) {
	if callTimeout <= 0 {
		return nil, errors.New("D-Bus call timeout must be positive")
	}
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("connect to system bus: %w", err)
	}
	return &DBusSystem{conn: conn, callTimeout: callTimeout}, nil
}

func (d *DBusSystem) Close() error {
	return d.conn.Close()
}

func (d *DBusSystem) DisplayManagerVersion(ctx context.Context) (string, error) {
	callCtx, cancel := d.withCallTimeout(ctx)
	defer cancel()

	obj := d.conn.Object(gdmName, gdmManagerPath)
	var variant dbus.Variant
	if err := obj.CallWithContext(callCtx, propertiesInterface+".Get", dbus.FlagNoAutoStart,
		gdmManager, "Version").Store(&variant); err != nil {
		return "", err
	}
	var version string
	if err := variant.Store(&version); err != nil {
		return "", fmt.Errorf("decode GDM Version property: %w", err)
	}
	if version == "" {
		return "", errors.New("GDM returned an empty Version property")
	}
	return version, nil
}

func (d *DBusSystem) CurrentSession(ctx context.Context, pid uint32) (Session, error) {
	path, err := d.sessionPathByPID(ctx, pid)
	if err != nil {
		if !dbusErrorNamed(err, "org.freedesktop.login1.NoSessionForPID") {
			return Session{}, err
		}
		// Desktop applications may be moved from session-*.scope into the
		// user's app.slice. "auto" asks logind for the D-Bus caller's own
		// session, or that user's display session when there is no direct
		// cgroup association.
		callCtx, cancel := d.withCallTimeout(ctx)
		obj := d.conn.Object(loginName, loginManagerPath)
		err = obj.CallWithContext(callCtx, loginManager+".GetSession", 0, "auto").Store(&path)
		cancel()
		if err != nil {
			return Session{}, fmt.Errorf("PID lookup failed and logind could not resolve the caller's display session: %w", err)
		}
	}
	if !path.IsValid() || path == "/" {
		return Session{}, fmt.Errorf("logind returned invalid session object path %q", path)
	}
	return d.session(ctx, path)
}

func (d *DBusSystem) sessionPathByPID(ctx context.Context, pid uint32) (dbus.ObjectPath, error) {
	callCtx, cancel := d.withCallTimeout(ctx)
	defer cancel()

	var path dbus.ObjectPath
	obj := d.conn.Object(loginName, loginManagerPath)
	if err := obj.CallWithContext(callCtx, loginManager+".GetSessionByPID", 0, pid).Store(&path); err != nil {
		return "", err
	}
	return path, nil
}

func (d *DBusSystem) SeatCapabilities(ctx context.Context, seatPath string) (SeatCapabilities, error) {
	path, err := objectPath(seatPath)
	if err != nil {
		return SeatCapabilities{}, err
	}
	props, err := d.getAll(ctx, loginName, path, loginSeat)
	if err != nil {
		return SeatCapabilities{}, err
	}
	canTTY, err := property[bool](props, "CanTTY")
	if err != nil {
		return SeatCapabilities{}, err
	}
	canGraphical, err := property[bool](props, "CanGraphical")
	if err != nil {
		return SeatCapabilities{}, err
	}
	return SeatCapabilities{CanTTY: canTTY, CanGraphical: canGraphical}, nil
}

func (d *DBusSystem) ActiveSession(ctx context.Context, seatPath string) (string, error) {
	path, err := objectPath(seatPath)
	if err != nil {
		return "", err
	}
	props, err := d.getAll(ctx, loginName, path, loginSeat)
	if err != nil {
		return "", err
	}
	ref, err := property[sessionRef](props, "ActiveSession")
	if err != nil {
		return "", err
	}
	return ref.ID, nil
}

func (d *DBusSystem) FindGreeter(ctx context.Context, seatPath string) (Session, bool, error) {
	path, err := objectPath(seatPath)
	if err != nil {
		return Session{}, false, err
	}
	props, err := d.getAll(ctx, loginName, path, loginSeat)
	if err != nil {
		return Session{}, false, err
	}
	refs, err := property[[]sessionRef](props, "Sessions")
	if err != nil {
		return Session{}, false, err
	}

	for _, ref := range refs {
		session, err := d.session(ctx, ref.Path)
		if err != nil {
			if dbusObjectDisappeared(err) {
				continue
			}
			return Session{}, false, fmt.Errorf("inspect seat session %q: %w", ref.ID, err)
		}
		if session.Class == "greeter" &&
			session.Service == "gdm-launch-environment" &&
			session.State != "closing" {
			return session, true, nil
		}
	}
	return Session{}, false, nil
}

func (d *DBusSystem) LockSession(ctx context.Context, sessionID string) error {
	return d.call(ctx, loginName, loginManagerPath, loginManager+".LockSession", sessionID)
}

func (d *DBusSystem) PrepareLockConfirmation(ctx context.Context, session Session) (LockConfirmation, error) {
	path, err := objectPath(session.Path)
	if err != nil {
		return nil, err
	}
	rule := []dbus.MatchOption{
		dbus.WithMatchSender(loginName),
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface(propertiesInterface),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, loginSession),
	}
	signals := make(chan *dbus.Signal, 16)
	d.conn.Signal(signals)
	callCtx, cancel := d.withCallTimeout(ctx)
	err = d.conn.AddMatchSignalContext(callCtx, rule...)
	cancel()
	if err != nil {
		d.conn.RemoveSignal(signals)
		return nil, fmt.Errorf("subscribe to logind lock state: %w", err)
	}

	waylandCtx, cancelWayland := d.withCallTimeout(ctx)
	wayland, waylandErr := newHyprlandLockWatcher(waylandCtx)
	cancelWayland()
	if wayland != nil {
		peerSessionPath, err := d.sessionPathByPID(ctx, wayland.PeerPID())
		if err != nil {
			waylandErr = fmt.Errorf("map Hyprland peer PID %d to logind session: %w", wayland.PeerPID(), err)
			_ = wayland.Close()
			wayland = nil
		} else if string(peerSessionPath) != session.Path {
			waylandErr = fmt.Errorf("Hyprland Wayland peer belongs to %q, caller resolved to %q", peerSessionPath, session.Path)
			_ = wayland.Close()
			wayland = nil
		}
	}

	return &dbusLockConfirmation{
		system:     d,
		session:    session,
		rule:       rule,
		signals:    signals,
		wayland:    wayland,
		waylandErr: waylandErr,
	}, nil
}

func (d *DBusSystem) sessionLocked(ctx context.Context, session Session) (bool, error) {
	path, err := objectPath(session.Path)
	if err != nil {
		return false, err
	}
	props, err := d.getAll(ctx, loginName, path, loginSession)
	if err != nil {
		return false, err
	}
	return property[bool](props, "LockedHint")
}

type dbusLockConfirmation struct {
	system  *DBusSystem
	session Session
	rule    []dbus.MatchOption
	signals chan *dbus.Signal
	wayland *hyprlandLockWatcher

	waylandErr error
	logindErr  error
	source     string
	close      sync.Once
}

func (c *dbusLockConfirmation) Wait(ctx context.Context) (string, error) {
	for {
		locked, err := c.system.sessionLocked(ctx, c.session)
		if err != nil {
			c.logindErr = err
		} else {
			c.logindErr = nil
			if locked {
				c.source = "logind LockedHint"
				return c.source, nil
			}
		}

		if c.wayland != nil {
			locked, err = c.wayland.State()
			if err != nil {
				c.waylandErr = err
				_ = c.wayland.Close()
				c.wayland = nil
			} else if locked {
				c.source = "Hyprland lock-notify"
				return c.source, nil
			}
		}

		var waylandEvents <-chan struct{}
		var waylandErrors <-chan error
		if c.wayland != nil {
			waylandEvents = c.wayland.Events()
			waylandErrors = c.wayland.Errors()
		}
		select {
		case <-ctx.Done():
			return "", c.timeoutError(ctx.Err())
		case _, open := <-c.signals:
			if !open {
				return "", errors.New("logind lock signal channel closed")
			}
		case <-waylandEvents:
		case err := <-waylandErrors:
			c.waylandErr = err
			_ = c.wayland.Close()
			c.wayland = nil
		}
	}
}

func (c *dbusLockConfirmation) StillLocked(ctx context.Context) (bool, error) {
	switch c.source {
	case "logind LockedHint":
		return c.system.sessionLocked(ctx, c.session)
	case "Hyprland lock-notify":
		if c.wayland == nil {
			return false, errors.New("Hyprland lock-notify connection was lost")
		}
		return c.wayland.State()
	default:
		return false, errors.New("session lock has not been confirmed")
	}
}

func (c *dbusLockConfirmation) Close() {
	c.close.Do(func() {
		if c.wayland != nil {
			_ = c.wayland.Close()
		}
		c.system.conn.RemoveSignal(c.signals)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), c.system.callTimeout)
		defer cancel()
		_ = c.system.conn.RemoveMatchSignalContext(cleanupCtx, c.rule...)
	})
}

func (c *dbusLockConfirmation) timeoutError(timeout error) error {
	message := timeout.Error() + "; logind LockedHint remained false"
	if c.logindErr != nil {
		message = timeout.Error() + "; last logind LockedHint read failed: " + c.logindErr.Error()
	}
	if c.waylandErr != nil {
		message += "; Hyprland lock-notify unavailable: " + c.waylandErr.Error()
	}
	return errors.New(message)
}

func (d *DBusSystem) ActivateSession(ctx context.Context, sessionID, seatID string) error {
	return d.call(ctx, loginName, loginManagerPath,
		loginManager+".ActivateSessionOnSeat", sessionID, seatID)
}

func (d *DBusSystem) SwitchToVT(ctx context.Context, seatPath string, vt uint32) error {
	path, err := objectPath(seatPath)
	if err != nil {
		return err
	}
	return d.call(ctx, loginName, path, loginSeat+".SwitchTo", vt)
}

func (d *DBusSystem) WaitGreeterActive(ctx context.Context, seatPath string) (Session, error) {
	path, err := objectPath(seatPath)
	if err != nil {
		return Session{}, err
	}
	rules := [][]dbus.MatchOption{
		{
			dbus.WithMatchSender(loginName),
			dbus.WithMatchObjectPath(loginManagerPath),
			dbus.WithMatchInterface(loginManager),
			dbus.WithMatchMember("SessionNew"),
		},
		{
			dbus.WithMatchSender(loginName),
			dbus.WithMatchObjectPath(path),
			dbus.WithMatchInterface(propertiesInterface),
			dbus.WithMatchMember("PropertiesChanged"),
			dbus.WithMatchArg(0, loginSeat),
		},
	}

	var active Session
	err = d.waitForSignals(ctx, rules, func(checkCtx context.Context) (bool, error) {
		greeter, found, err := d.FindGreeter(checkCtx, seatPath)
		if err != nil || !found {
			return false, err
		}
		activeID, err := d.ActiveSession(checkCtx, seatPath)
		if err != nil {
			return false, err
		}
		if activeID != greeter.ID {
			return false, nil
		}
		active = greeter
		return true, nil
	})
	if err != nil {
		return Session{}, err
	}
	return active, nil
}

type sessionRef struct {
	ID   string
	Path dbus.ObjectPath
}

type seatRef struct {
	ID   string
	Path dbus.ObjectPath
}

func (d *DBusSystem) session(ctx context.Context, path dbus.ObjectPath) (Session, error) {
	props, err := d.getAll(ctx, loginName, path, loginSession)
	if err != nil {
		return Session{}, err
	}
	return decodeSession(path, props)
}

func decodeSession(path dbus.ObjectPath, props map[string]dbus.Variant) (Session, error) {
	id, err := property[string](props, "Id")
	if err != nil {
		return Session{}, err
	}
	seat, err := property[seatRef](props, "Seat")
	if err != nil {
		return Session{}, err
	}
	class, err := property[string](props, "Class")
	if err != nil {
		return Session{}, err
	}
	service, err := property[string](props, "Service")
	if err != nil {
		return Session{}, err
	}
	typeName, err := property[string](props, "Type")
	if err != nil {
		return Session{}, err
	}
	state, err := property[string](props, "State")
	if err != nil {
		return Session{}, err
	}
	tty, err := property[string](props, "TTY")
	if err != nil {
		return Session{}, err
	}
	vt, err := property[uint32](props, "VTNr")
	if err != nil {
		return Session{}, err
	}
	active, err := property[bool](props, "Active")
	if err != nil {
		return Session{}, err
	}
	locked, err := property[bool](props, "LockedHint")
	if err != nil {
		return Session{}, err
	}
	remote, err := property[bool](props, "Remote")
	if err != nil {
		return Session{}, err
	}

	return Session{
		ID:       id,
		Path:     string(path),
		SeatID:   seat.ID,
		SeatPath: string(seat.Path),
		Class:    class,
		Service:  service,
		Type:     typeName,
		State:    state,
		TTY:      tty,
		VT:       vt,
		Active:   active,
		Locked:   locked,
		Remote:   remote,
	}, nil
}

func (d *DBusSystem) getAll(ctx context.Context, name string, path dbus.ObjectPath, iface string) (map[string]dbus.Variant, error) {
	callCtx, cancel := d.withCallTimeout(ctx)
	defer cancel()

	var props map[string]dbus.Variant
	obj := d.conn.Object(name, path)
	if err := obj.CallWithContext(callCtx, propertiesInterface+".GetAll", 0, iface).Store(&props); err != nil {
		return nil, err
	}
	return props, nil
}

func (d *DBusSystem) call(ctx context.Context, name string, path dbus.ObjectPath, method string, args ...any) error {
	callCtx, cancel := d.withCallTimeout(ctx)
	defer cancel()
	return d.conn.Object(name, path).CallWithContext(callCtx, method, 0, args...).Err
}

func (d *DBusSystem) withCallTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d.callTimeout)
}

func (d *DBusSystem) waitForSignals(
	ctx context.Context,
	rules [][]dbus.MatchOption,
	check func(context.Context) (bool, error),
) error {
	signals := make(chan *dbus.Signal, 16)
	d.conn.Signal(signals)
	defer d.conn.RemoveSignal(signals)

	added := 0
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), d.callTimeout)
		defer cancel()
		for i := added - 1; i >= 0; i-- {
			_ = d.conn.RemoveMatchSignalContext(cleanupCtx, rules[i]...)
		}
	}()

	for _, rule := range rules {
		callCtx, cancel := d.withCallTimeout(ctx)
		err := d.conn.AddMatchSignalContext(callCtx, rule...)
		cancel()
		if err != nil {
			return fmt.Errorf("subscribe to D-Bus signal: %w", err)
		}
		added++
	}

	for {
		ok, err := check(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal, open := <-signals:
			if !open {
				return errors.New("D-Bus signal channel closed")
			}
			if signal == nil {
				return errors.New("received nil D-Bus signal")
			}
		}
	}
}

func property[T any](props map[string]dbus.Variant, name string) (T, error) {
	var value T
	variant, ok := props[name]
	if !ok {
		return value, fmt.Errorf("D-Bus property %q is missing", name)
	}
	if err := variant.Store(&value); err != nil {
		return value, fmt.Errorf("decode D-Bus property %q (%s): %w", name, variant.Signature(), err)
	}
	return value, nil
}

func objectPath(path string) (dbus.ObjectPath, error) {
	value := dbus.ObjectPath(path)
	if !value.IsValid() || value == "/" {
		return "", fmt.Errorf("invalid D-Bus object path %q", path)
	}
	return value, nil
}

func dbusObjectDisappeared(err error) bool {
	return dbusErrorNamed(err,
		"org.freedesktop.DBus.Error.UnknownObject",
		"org.freedesktop.login1.NoSuchSession",
	)
}

func dbusErrorNamed(err error, names ...string) bool {
	var errorName string
	var pointer *dbus.Error
	if errors.As(err, &pointer) {
		errorName = pointer.Name
	} else {
		var value dbus.Error
		if !errors.As(err, &value) {
			return false
		}
		errorName = value.Name
	}
	for _, name := range names {
		if errorName == name {
			return true
		}
	}
	return false
}
