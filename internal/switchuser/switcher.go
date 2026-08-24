package switchuser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// Session is the subset of a logind session used by the switcher.
type Session struct {
	ID       string
	Path     string
	SeatID   string
	SeatPath string
	Class    string
	Service  string
	Type     string
	State    string
	TTY      string
	VT       uint32
	Active   bool
	Locked   bool
	Remote   bool
}

// SeatCapabilities describes whether logind can switch graphical VTs on a
// seat.
type SeatCapabilities struct {
	CanTTY       bool
	CanGraphical bool
}

// LockConfirmation is armed before LockSession is called so no asynchronous
// compositor or logind event can be missed.
type LockConfirmation interface {
	Wait(context.Context) (source string, err error)
	StillLocked(context.Context) (bool, error)
	Close()
}

// System isolates all D-Bus operations from the switching policy.
//
// LockConfirmation and WaitGreeterActive are event-driven; their contexts
// provide the upper time bounds.
type System interface {
	DisplayManagerVersion(context.Context) (string, error)
	CurrentSession(context.Context, uint32) (Session, error)
	SeatCapabilities(context.Context, string) (SeatCapabilities, error)
	ActiveSession(context.Context, string) (string, error)
	FindGreeter(context.Context, string) (Session, bool, error)
	PrepareLockConfirmation(context.Context, Session) (LockConfirmation, error)
	LockSession(context.Context, string) error
	ActivateSession(context.Context, string, string) error
	SwitchToVT(context.Context, string, uint32) error
	WaitGreeterActive(context.Context, string) (Session, error)
}

// Config controls time-bounded confirmation and the GDM initial VT.
type Config struct {
	GreeterVT       uint32
	LockTimeout     time.Duration
	GreeterTimeout  time.Duration
	RecoveryTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		GreeterVT:       1,
		LockTimeout:     5 * time.Second,
		GreeterTimeout:  15 * time.Second,
		RecoveryTimeout: 5 * time.Second,
	}
}

func (c Config) validate() error {
	if c.GreeterVT == 0 || c.GreeterVT > 63 {
		return fmt.Errorf("greeter VT must be between 1 and 63, got %d", c.GreeterVT)
	}
	if c.LockTimeout <= 0 {
		return errors.New("lock timeout must be positive")
	}
	if c.GreeterTimeout <= 0 {
		return errors.New("greeter timeout must be positive")
	}
	if c.RecoveryTimeout <= 0 {
		return errors.New("recovery timeout must be positive")
	}
	return nil
}

type Switcher struct {
	system System
	config Config
	logger *slog.Logger
}

func New(system System, config Config, logger *slog.Logger) (*Switcher, error) {
	if system == nil {
		return nil, errors.New("system D-Bus client is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Switcher{system: system, config: config, logger: logger}, nil
}

// Run locks the caller's active GDM-managed session, confirms that lock via
// logind, and only then transfers the seat to an existing GDM greeter or GDM's
// initial VT.
func (s *Switcher) Run(ctx context.Context, pid uint32) error {
	version, err := s.system.DisplayManagerVersion(ctx)
	if err != nil {
		return fmt.Errorf("GDM is not available on the system bus: %w", err)
	}
	if version != "50" && !strings.HasPrefix(version, "50.") {
		return fmt.Errorf("GDM version %q is unsupported; this initial-VT mechanism was verified for GDM 50", version)
	}
	s.logger.Debug("connected to GDM", "version", version)

	current, err := s.system.CurrentSession(ctx, pid)
	if err != nil {
		return fmt.Errorf("identify caller's logind session: %w", err)
	}
	if err := validateCurrentSession(current, s.config.GreeterVT); err != nil {
		return err
	}
	s.logger.Debug("resolved caller session",
		"session", current.ID,
		"seat", current.SeatID,
		"tty", current.TTY,
		"type", current.Type,
		"service", current.Service)

	capabilities, err := s.system.SeatCapabilities(ctx, current.SeatPath)
	if err != nil {
		return fmt.Errorf("inspect logind seat %q: %w", current.SeatID, err)
	}
	if !capabilities.CanTTY || !capabilities.CanGraphical {
		return fmt.Errorf("seat %q cannot switch graphical VTs (CanTTY=%t, CanGraphical=%t)",
			current.SeatID, capabilities.CanTTY, capabilities.CanGraphical)
	}
	if err := s.requireActive(ctx, current); err != nil {
		return err
	}

	confirmation, err := s.system.PrepareLockConfirmation(ctx, current)
	if err != nil {
		return fmt.Errorf("prepare session-lock confirmation: %w", err)
	}
	defer confirmation.Close()

	s.logger.Info("requesting session lock", "session", current.ID, "tty", current.TTY)
	if err := s.system.LockSession(ctx, current.ID); err != nil {
		return fmt.Errorf("request logind lock for session %q: %w", current.ID, err)
	}

	lockCtx, cancelLock := context.WithTimeout(ctx, s.config.LockTimeout)
	confirmationSource, err := confirmation.Wait(lockCtx)
	cancelLock()
	if err != nil {
		return fmt.Errorf("the compositor/logind did not confirm session %q locked: %w; refusing to switch away",
			current.ID, err)
	}

	// Re-read both facts immediately before switching. This closes the gap
	// between receiving the lock notification and making the VT active change.
	locked, err := confirmation.StillLocked(ctx)
	if err != nil {
		return fmt.Errorf("recheck lock for session %q: %w; refusing to switch away", current.ID, err)
	}
	if !locked {
		return fmt.Errorf("session %q is no longer reported locked; refusing to switch away", current.ID)
	}
	if err := s.requireActive(ctx, current); err != nil {
		return fmt.Errorf("%w; refusing to switch away", err)
	}
	s.logger.Info("session lock confirmed", "session", current.ID, "source", confirmationSource)

	greeter, found, err := s.system.FindGreeter(ctx, current.SeatPath)
	if err != nil {
		return fmt.Errorf("look for an existing GDM greeter: %w", err)
	}
	if found {
		s.logger.Info("activating existing GDM greeter", "session", greeter.ID, "tty", greeter.TTY)
		if err := s.system.ActivateSession(ctx, greeter.ID, current.SeatID); err != nil {
			return fmt.Errorf("activate GDM greeter session %q: %w", greeter.ID, err)
		}
	} else {
		s.logger.Info("switching to GDM initial VT", "vt", s.config.GreeterVT)
		if err := s.system.SwitchToVT(ctx, current.SeatPath, s.config.GreeterVT); err != nil {
			return fmt.Errorf("switch seat %q to VT %d: %w", current.SeatID, s.config.GreeterVT, err)
		}
	}

	greeterCtx, cancelGreeter := context.WithTimeout(ctx, s.config.GreeterTimeout)
	activeGreeter, err := s.system.WaitGreeterActive(greeterCtx, current.SeatPath)
	cancelGreeter()
	if err != nil {
		return s.recoverAfterGreeterFailure(current, err)
	}
	s.logger.Info("GDM greeter is active", "session", activeGreeter.ID, "tty", activeGreeter.TTY)
	return nil
}

func validateCurrentSession(session Session, greeterVT uint32) error {
	if session.ID == "" || session.Path == "" {
		return errors.New("logind returned an incomplete caller session")
	}
	if session.SeatID != "seat0" || session.SeatPath == "" {
		return fmt.Errorf("session %q is on seat %q; the GDM initial-VT mechanism is only valid on seat0",
			session.ID, session.SeatID)
	}
	if session.Class != "user" {
		return fmt.Errorf("session %q has logind class %q, not a user session", session.ID, session.Class)
	}
	if session.Remote {
		return fmt.Errorf("session %q is remote; refusing a local VT switch", session.ID)
	}
	if !session.Active {
		return fmt.Errorf("session %q is not the active session", session.ID)
	}
	if session.VT == 0 {
		return fmt.Errorf("session %q is not attached to a VT", session.ID)
	}
	if session.VT == greeterVT {
		return fmt.Errorf("session %q is already on configured GDM initial VT %d", session.ID, greeterVT)
	}
	if !strings.HasPrefix(session.Service, "gdm-") {
		return fmt.Errorf("session %q was created by %q, not GDM", session.ID, session.Service)
	}
	return nil
}

func (s *Switcher) requireActive(ctx context.Context, current Session) error {
	activeID, err := s.system.ActiveSession(ctx, current.SeatPath)
	if err != nil {
		return fmt.Errorf("read active session on seat %q: %w", current.SeatID, err)
	}
	if activeID != current.ID {
		return fmt.Errorf("seat %q active session changed from %q to %q", current.SeatID, current.ID, activeID)
	}
	return nil
}

func (s *Switcher) recoverAfterGreeterFailure(current Session, waitErr error) error {
	recoveryCtx, cancel := context.WithTimeout(context.Background(), s.config.RecoveryTimeout)
	defer cancel()

	if err := s.system.ActivateSession(recoveryCtx, current.ID, current.SeatID); err != nil {
		return fmt.Errorf("GDM greeter did not become active: %w; also failed to return to locked session %q: %v",
			waitErr, current.ID, err)
	}
	return fmt.Errorf("GDM greeter did not become active: %w; returned to locked session %q",
		waitErr, current.ID)
}
