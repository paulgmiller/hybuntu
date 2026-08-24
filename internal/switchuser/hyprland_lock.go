package switchuser

// This file implements the tiny subset of the Wayland wire protocol needed to
// consume Hyprland's hyprland-lock-notify-v1 protocol. The protocol guarantees
// that "locked" is emitted only after the lock client has presented a frame on
// every output. Keeping this client local avoids invoking hyprctl and works with
// Ubuntu 26.04's Hypridle 0.1.7, which does not yet expose ScreenSaver.GetActive.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

const (
	waylandDisplayID       = 1
	waylandRegistryID      = 2
	waylandCallbackID      = 3
	hyprLockNotifierID     = 4
	hyprLockNotificationID = 5

	hyprLockNotifierInterface = "hyprland_lock_notifier_v1"
	maxWaylandMessageSize     = 65532
)

type hyprlandLockWatcher struct {
	conn    net.Conn
	peerPID uint32
	peerUID uint32

	writeMu sync.Mutex
	close   sync.Once
	ready   chan struct{}
	events  chan struct{}
	errors  chan error

	interfaceFound atomic.Bool
	locked         atomic.Bool
	closed         atomic.Bool
}

func newHyprlandLockWatcher(ctx context.Context) (*hyprlandLockWatcher, error) {
	socket, err := waylandSocketPath()
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("connect to Hyprland Wayland socket %q: %w", socket, err)
	}

	peerPID, peerUID, err := waylandPeerCredentials(conn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("identify Hyprland Wayland peer: %w", err)
	}
	if peerUID != uint32(os.Geteuid()) {
		_ = conn.Close()
		return nil, fmt.Errorf("Hyprland Wayland peer has uid %d, caller has uid %d", peerUID, os.Geteuid())
	}

	w := &hyprlandLockWatcher{
		conn:    conn,
		peerPID: peerPID,
		peerUID: peerUID,
		ready:   make(chan struct{}),
		events:  make(chan struct{}, 1),
		errors:  make(chan error, 1),
	}
	if err := w.writeRequest(waylandDisplayID, 1, uint32Payload(waylandRegistryID)); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("request Wayland registry: %w", err)
	}
	if err := w.writeRequest(waylandDisplayID, 0, uint32Payload(waylandCallbackID)); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("request Wayland registry round trip: %w", err)
	}

	go w.readLoop()
	select {
	case <-w.ready:
		if !w.interfaceFound.Load() {
			_ = w.Close()
			return nil, errors.New("Hyprland does not advertise hyprland-lock-notify-v1")
		}
		return w, nil
	case err := <-w.errors:
		_ = w.Close()
		return nil, err
	case <-ctx.Done():
		_ = w.Close()
		return nil, ctx.Err()
	}
}

func (w *hyprlandLockWatcher) Events() <-chan struct{} { return w.events }

func (w *hyprlandLockWatcher) Errors() <-chan error { return w.errors }

func (w *hyprlandLockWatcher) PeerPID() uint32 { return w.peerPID }

func (w *hyprlandLockWatcher) State() (bool, error) {
	select {
	case err := <-w.errors:
		return false, err
	default:
		return w.locked.Load(), nil
	}
}

func (w *hyprlandLockWatcher) Close() error {
	var err error
	w.close.Do(func() {
		w.closed.Store(true)
		err = w.conn.Close()
	})
	return err
}

func (w *hyprlandLockWatcher) readLoop() {
	for {
		sender, opcode, payload, err := readWaylandMessage(w.conn)
		if err != nil {
			if !w.closed.Load() {
				w.reportError(fmt.Errorf("read Hyprland lock notification: %w", err))
			}
			return
		}
		if err := w.handleMessage(sender, opcode, payload); err != nil {
			w.reportError(err)
			return
		}
	}
}

func (w *hyprlandLockWatcher) handleMessage(sender, opcode uint32, payload []byte) error {
	switch sender {
	case waylandDisplayID:
		if opcode == 0 {
			return decodeWaylandDisplayError(payload)
		}
	case waylandRegistryID:
		if opcode != 0 {
			return nil
		}
		name, iface, _, err := decodeWaylandGlobal(payload)
		if err != nil {
			return err
		}
		if iface != hyprLockNotifierInterface || w.interfaceFound.Swap(true) {
			return nil
		}
		if err := w.bindLockNotifier(name); err != nil {
			return err
		}
	case waylandCallbackID:
		if opcode == 0 {
			select {
			case <-w.ready:
			default:
				close(w.ready)
			}
		}
	case hyprLockNotificationID:
		switch opcode {
		case 0:
			w.locked.Store(true)
			w.notifyStateChange()
		case 1:
			w.locked.Store(false)
			w.notifyStateChange()
		default:
			return fmt.Errorf("unexpected hyprland-lock-notify event opcode %d", opcode)
		}
	}
	return nil
}

func (w *hyprlandLockWatcher) bindLockNotifier(globalName uint32) error {
	payload := make([]byte, 0, 32+len(hyprLockNotifierInterface))
	payload = appendUint32(payload, globalName)
	payload = appendWaylandString(payload, hyprLockNotifierInterface)
	payload = appendUint32(payload, 1)
	payload = appendUint32(payload, hyprLockNotifierID)
	if err := w.writeRequest(waylandRegistryID, 0, payload); err != nil {
		return fmt.Errorf("bind %s: %w", hyprLockNotifierInterface, err)
	}
	if err := w.writeRequest(hyprLockNotifierID, 1, uint32Payload(hyprLockNotificationID)); err != nil {
		return fmt.Errorf("create Hyprland lock notification: %w", err)
	}
	return nil
}

func (w *hyprlandLockWatcher) writeRequest(sender, opcode uint32, payload []byte) error {
	message, err := encodeWaylandMessage(sender, opcode, payload)
	if err != nil {
		return err
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	var written int
	written, err = w.conn.Write(message)
	if err == nil && written != len(message) {
		return io.ErrShortWrite
	}
	return err
}

func (w *hyprlandLockWatcher) notifyStateChange() {
	select {
	case w.events <- struct{}{}:
	default:
	}
}

func (w *hyprlandLockWatcher) reportError(err error) {
	select {
	case w.errors <- err:
	default:
	}
}

func waylandSocketPath() (string, error) {
	display := os.Getenv("WAYLAND_DISPLAY")
	if display == "" {
		display = "wayland-0"
	}
	if strings.ContainsRune(display, 0) {
		return "", errors.New("WAYLAND_DISPLAY contains a NUL byte")
	}
	if filepath.IsAbs(display) {
		return display, nil
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", errors.New("XDG_RUNTIME_DIR is not set")
	}
	return filepath.Join(runtimeDir, display), nil
}

func waylandPeerCredentials(conn net.Conn) (uint32, uint32, error) {
	syscallConn, ok := conn.(syscall.Conn)
	if !ok {
		return 0, 0, errors.New("Wayland connection does not expose socket credentials")
	}
	raw, err := syscallConn.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var credentials *syscall.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, 0, err
	}
	if socketErr != nil {
		return 0, 0, socketErr
	}
	if credentials == nil || credentials.Pid <= 0 {
		return 0, 0, errors.New("Wayland socket returned invalid peer credentials")
	}
	return uint32(credentials.Pid), credentials.Uid, nil
}

func readWaylandMessage(reader io.Reader) (sender, opcode uint32, payload []byte, err error) {
	var header [8]byte
	if _, err = io.ReadFull(reader, header[:]); err != nil {
		return 0, 0, nil, err
	}
	sender = binary.NativeEndian.Uint32(header[0:4])
	sizeAndOpcode := binary.NativeEndian.Uint32(header[4:8])
	opcode = sizeAndOpcode & 0xffff
	size := sizeAndOpcode >> 16
	if size < 8 || size%4 != 0 || size > maxWaylandMessageSize {
		return 0, 0, nil, fmt.Errorf("invalid Wayland message size %d", size)
	}
	payload = make([]byte, size-8)
	if _, err = io.ReadFull(reader, payload); err != nil {
		return 0, 0, nil, err
	}
	return sender, opcode, payload, nil
}

func encodeWaylandMessage(sender, opcode uint32, payload []byte) ([]byte, error) {
	size := 8 + len(payload)
	if size%4 != 0 || size > maxWaylandMessageSize || opcode > 0xffff {
		return nil, fmt.Errorf("invalid Wayland request (opcode=%d, size=%d)", opcode, size)
	}
	message := make([]byte, size)
	binary.NativeEndian.PutUint32(message[0:4], sender)
	binary.NativeEndian.PutUint32(message[4:8], uint32(size)<<16|opcode)
	copy(message[8:], payload)
	return message, nil
}

func decodeWaylandGlobal(payload []byte) (name uint32, iface string, version uint32, err error) {
	if len(payload) < 12 {
		return 0, "", 0, errors.New("short wl_registry.global event")
	}
	name = binary.NativeEndian.Uint32(payload[0:4])
	iface, used, err := decodeWaylandString(payload[4:])
	if err != nil {
		return 0, "", 0, fmt.Errorf("decode wl_registry.global interface: %w", err)
	}
	offset := 4 + used
	if len(payload) < offset+4 {
		return 0, "", 0, errors.New("short wl_registry.global version")
	}
	version = binary.NativeEndian.Uint32(payload[offset : offset+4])
	return name, iface, version, nil
}

func decodeWaylandDisplayError(payload []byte) error {
	if len(payload) < 12 {
		return errors.New("Hyprland sent a short wl_display.error event")
	}
	objectID := binary.NativeEndian.Uint32(payload[0:4])
	code := binary.NativeEndian.Uint32(payload[4:8])
	message, _, err := decodeWaylandString(payload[8:])
	if err != nil {
		return fmt.Errorf("Hyprland Wayland protocol error on object %d (code %d)", objectID, code)
	}
	return fmt.Errorf("Hyprland Wayland protocol error on object %d (code %d): %s", objectID, code, message)
}

func decodeWaylandString(data []byte) (string, int, error) {
	if len(data) < 4 {
		return "", 0, errors.New("short Wayland string length")
	}
	length := int(binary.NativeEndian.Uint32(data[0:4]))
	if length < 1 {
		return "", 0, errors.New("invalid empty Wayland string encoding")
	}
	padded := (length + 3) &^ 3
	if padded > len(data)-4 {
		return "", 0, errors.New("Wayland string exceeds message")
	}
	value := data[4 : 4+length]
	if value[length-1] != 0 {
		return "", 0, errors.New("Wayland string is not NUL-terminated")
	}
	return string(value[:length-1]), 4 + padded, nil
}

func uint32Payload(value uint32) []byte {
	payload := make([]byte, 4)
	binary.NativeEndian.PutUint32(payload, value)
	return payload
}

func appendUint32(dst []byte, value uint32) []byte {
	var encoded [4]byte
	binary.NativeEndian.PutUint32(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendWaylandString(dst []byte, value string) []byte {
	length := len(value) + 1
	dst = appendUint32(dst, uint32(length))
	dst = append(dst, value...)
	dst = append(dst, 0)
	for len(dst)%4 != 0 {
		dst = append(dst, 0)
	}
	return dst
}
