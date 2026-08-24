# hypr-switch-user

`hypr-switch-user` provides a conservative “Switch User” action for a
Hyprland session launched by GDM on Ubuntu 26.04. It uses D-Bus and Hyprland's
lock-notify Wayland protocol directly from Go; it does not execute `loginctl`,
`gdbus`, `hyprctl`, or a fixed sleep.

The tool deliberately does **not** call
`org.gnome.DisplayManager.LocalDisplayFactory.CreateTransientDisplay`. On the
target machine, that path allowed a subsequent login to terminate the original
Hyprland session.

## Mechanism

The operation is ordered as follows:

1. Read `org.gnome.DisplayManager.Manager.Version` to ensure GDM owns its
   system-bus API. Refuse versions outside the researched GDM 50 series.
2. Resolve the caller with logind's `GetSessionByPID`. systemd may move desktop
   apps into `user@.service/app.slice`; if that removes the direct session
   association, fall back to `GetSession("auto")`, which resolves the D-Bus
   caller's display session. Then require an active, local, GDM-created `user`
   session on graphical `seat0`.
3. Before requesting a lock, arm two event-driven confirmations: the logind
   session's `PropertiesChanged` signal and Hyprland's
   `hyprland-lock-notify-v1` Wayland protocol.
4. Call `org.freedesktop.login1.Manager.LockSession` for that exact session.
   Continue only after either logind reports `LockedHint=true`, or Hyprland
   emits `locked`. The Hyprland protocol specifies that this event comes after
   the lock client has presented a lock-screen frame on every output. The same
   source is rechecked, and the seat's `ActiveSession` is rechecked,
   immediately before any seat change.

The Wayland socket's kernel peer credentials are checked, and the compositor
PID must map through logind to the same session being locked. A notification
from another Hyprland session therefore cannot satisfy the safety gate.
5. If a non-closing session with class `greeter` and service
   `gdm-launch-environment` already exists, activate it with logind's
   `ActivateSessionOnSeat`.
6. Otherwise call `org.freedesktop.login1.Seat.SwitchTo(1)`. GDM 50 monitors
   active-VT changes; when its empty initial VT becomes active it runs
   `ensure_display_for_seat()` and creates (or activates) the normal greeter.
7. Wait on logind `SessionNew`/`PropertiesChanged` signals until the GDM
   greeter is the seat's active session. If that confirmation times out, the
   tool attempts to reactivate the original session, which remains locked.

No session creation, termination, or user-session activation is requested from
GDM. The existing Hyprland process tree remains registered with logind and GDM
on its original VT.

### Why VT 1

GDM's initial VT is a compile-time setting, not a public D-Bus property. The
Ubuntu 26.04 GDM 50.1 package installed on the test machine reserves
`getty@tty1.service`, and upstream defaults `GDM_INITIAL_VT` to 1. The user
sessions observed on that machine are on tty2 and tty3.

If GDM was locally rebuilt with a different initial VT, pass
`--greeter-vt N`. Switching to an empty initial VT is a GDM-specific behavior,
so this tool intentionally rejects non-`seat0`, remote, non-GDM, and non-VT
caller sessions.

## Locking requirement

`LockSession` is an asynchronous request: logind emits a `Lock` signal, and the
desktop's lock integration must act on it. A typical Hyprland setup runs
`hypridle` with a `lock_cmd` that starts `hyprlock`.

Stock Hyprlock 0.9.2
[does not set logind's `LockedHint`](https://github.com/hyprwm/hyprlock/issues/907),
so requiring only that property would make a safe switch impossible on the
target installation. The tool therefore also listens to Hyprland's
purpose-built lock-notify protocol.
If neither confirmation arrives, or if the confirmed state disappears before
the seat change, the program times out and **does not switch away**.

`LockedHint=true` remains supported for other lock clients. The compositor
event is stronger for Hyprlock because its contract includes a presented frame
on every output. Test the lock path locally before binding the command to a key.

## Build and test

Ubuntu 26.04's packaged Go toolchain is sufficient.

```sh
make
make test
```

The binary is written to `bin/hypr-switch-user`.

## Live-test runbook

There are two distinct live tests below. The first is read-only and does not
lock the session or switch VTs. The second deliberately locks the current
session and transfers the seat to GDM.

Run these commands from a terminal inside the target Hyprland session. Do not
run the program through `sudo`, from SSH, from a text console, or from another
user's session: it intentionally verifies the D-Bus caller's own graphical
session.

### 1. Check the machine and lock path

Before testing:

- Have a second local user with a known password. The tool does not create one.
- Save work in both accounts.
- Know the machine's local VT shortcuts, normally `Ctrl+Alt+F1`, `F2`, and
  `F3`. On the researched machine GDM uses tty1 and user sessions use tty2/3.
- Keep an SSH connection from another machine if practical. It is useful for
  inspection and recovery but must not be used to launch the test.

Record the current session and VT:

```sh
printf 'XDG_SESSION_ID=%s\nWAYLAND_DISPLAY=%s\nXDG_RUNTIME_DIR=%s\n' \
  "$XDG_SESSION_ID" "$WAYLAND_DISPLAY" "$XDG_RUNTIME_DIR"
loginctl list-sessions
loginctl show-session "$XDG_SESSION_ID" \
  -p Id -p Name -p Seat -p TTY -p Active -p Class -p Service -p State
loginctl show-seat seat0 -p ActiveSession -p CanTTY -p CanGraphical
```

The session should be active, local, class `user`, on `seat0`, have a real
`TTY=ttyN`, and normally have a service beginning with `gdm-`. If
`XDG_SESSION_ID` is empty, take the matching ID from `loginctl list-sessions`
for these diagnostic commands; the program itself can ask logind for the
caller's display session.

Next verify that logind's lock request actually starts the configured lock
client. Substitute the recorded session ID if necessary:

```sh
loginctl lock-session "$XDG_SESSION_ID"
```

Every connected output should show hyprlock. Unlock normally and return to the
same terminal. This manual `loginctl` command is only a diagnostic; the Go
program calls logind directly over D-Bus and never executes `loginctl`.

If no lock screen appears, stop here. Fix the Hyprland/hypridle handling of
logind's `Lock` signal before attempting the end-to-end test. A normal keybind
that starts hyprlock directly is not sufficient evidence, because the program
requests its lock through logind.

### 2. Run the read-only integration probes

From that same Hyprland terminal:

```sh
make build
make test
make live-test-readonly
```

The live target checks the installed GDM and logind interfaces, resolves the
current session and seat, finds any existing greeter, connects to the current
Hyprland Wayland socket, checks its kernel peer credentials, and verifies that
the compositor PID belongs to the same logind session. It does **not** call
`LockSession`, `Seat.SwitchTo`, or `ActivateSessionOnSeat`.

The expected live-test names are:

```text
TestLiveReadOnlySystemInterfaces
TestLiveHyprlandLockWatcher
```

Both must report `PASS`. Run the individual command below if more focused
output is useful:

```sh
HYPR_SWITCH_USER_LIVE_TEST=1 go test ./internal/switchuser -run '^TestLive' -v
```

### 3. Perform one end-to-end switch

Keep the original session ID and tty from step 1. Start the command directly
from the Hyprland terminal and save its diagnostic log:

```sh
./bin/hypr-switch-user --verbose 2>/tmp/hypr-switch-user-live.log
```

Expected behavior:

1. Hyprlock covers every output.
2. The display changes to GDM's ordinary user chooser on tty1.
3. Sign in as the second user. Do not select the already-running original user
   for this part of the test.
4. In the second user's session, inspect rather than activate the original
   session:

   ```sh
   loginctl list-sessions
   loginctl show-session ORIGINAL_SESSION_ID \
     -p Name -p TTY -p Active -p State -p Class -p Service
   ```

   Replace `ORIGINAL_SESSION_ID` with the value recorded in step 1. It should
   still exist on the same tty and should be inactive; the original Hyprland
   process and applications must not have been terminated.
5. Use that desktop's normal **Switch User** action, or log out of the second
   account, to return to GDM. Select the original user and authenticate.
6. The existing Hyprland session should resume with the same windows and the
   same logind session ID. Back in the original terminal, inspect the result:

   ```sh
   printf 'exit status: %s\n' "$?"
   cat /tmp/hypr-switch-user-live.log
   loginctl show-session "$XDG_SESSION_ID" \
     -p Id -p Name -p TTY -p Active -p State -p Class -p Service
   ```

A successful log contains `requesting session lock`, `session lock confirmed`,
either `activating existing GDM greeter` or `switching to GDM initial VT`, and
`GDM greeter is active`. The command exits zero.

Hyprlock 0.9.2 may continue to report `LockedHint=no`; that alone is not a test
failure. For Hyprlock, this program accepts the same-session compositor's
`hyprland-lock-notify-v1.locked` event, which is emitted only after a lock frame
has been presented on every output.

### Recovery and failure checks

- If lock confirmation fails, the program prints an error and must remain on
  the original VT. This is the primary fail-safe behavior.
- If GDM does not become active before `--greeter-timeout`, the program tries
  to reactivate the original session; that session remains locked.
- From the local keyboard, try the recorded original VT shortcut. Hyprlock
  should require authentication before the session can be used.
- From a pre-existing SSH connection, `loginctl list-sessions` can identify the
  original session. As an emergency, an administrator can run
  `sudo loginctl activate ORIGINAL_SESSION_ID`; this is a manual recovery path,
  not part of the program.
- Do not restart `gdm3` or `display-manager` as a recovery step: restarting the
  display manager can terminate the sessions this tool is designed to retain.
- If tty1 is not this machine's compiled GDM initial VT, rerun only after
  determining the correct value and pass `--greeter-vt N`. A wrong initial VT
  typically produces a blank/text VT followed by automatic reactivation after
  the greeter timeout.

Once the end-to-end test succeeds, installation is explicit; no target
modifies GDM, logind, PAM, polkit, Hyprland, or any other system configuration:

```sh
make install PREFIX="$HOME/.local"
```

Then bind it manually in Hyprland, for example:

```ini
bind = SUPER, L, exec, ~/.local/bin/hypr-switch-user
```

The repository's legacy `switch-user` file is now only a compatibility launcher
that executes the Go binary.

Useful options:

```text
--greeter-vt 1          GDM's compiled initial VT
--lock-timeout 5s       maximum lock-confirmation wait
--greeter-timeout 15s   maximum greeter activation wait
--dbus-timeout 3s       timeout for an individual D-Bus call
--verbose               include D-Bus/GDM diagnostic context
```

Errors are written to stderr and the command exits nonzero. In particular, a
lock request error, lock-confirmation timeout, lost lock hint, or change of the
active session prevents the VT switch.

## Interface research

Research and live introspection were performed against Ubuntu 26.04 packages
`gdm3 50.1-0ubuntu0.1`, `systemd 259.5-0ubuntu3.4`, `hypridle 0.1.7-2`, and
`hyprlock 0.9.2-1build4` on 2026-08-23.

- systemd documents `LockSession`, `ActivateSessionOnSeat`, `Seat.SwitchTo`,
  session `LockedHint`, and seat/session properties in the
  [logind D-Bus API](https://github.com/systemd/systemd/blob/v259/man/org.freedesktop.login1.xml).
- GDM's own user-switching helper first looks for a logind session whose class
  is `greeter` and service is `gdm-launch-environment`; see
  [GDM 50.1 `gdm-common.c`](https://github.com/GNOME/gdm/blob/50.1/common/gdm-common.c).
- GDM 50.1's local display factory watches the active VT and calls
  `ensure_display_for_seat()` on `GDM_INITIAL_VT`; see
  [GDM 50.1 `gdm-local-display-factory.c`](https://github.com/GNOME/gdm/blob/50.1/daemon/gdm-local-display-factory.c).
- Ubuntu publishes the target build as
  [`gdm3 50.1-0ubuntu0.1`](https://packages.ubuntu.com/resolute-updates/gdm3).
- Hyprland documents the compositor-confirmed `locked`/`unlocked` events in
  [`hyprland-lock-notify-v1.xml`](https://github.com/hyprwm/hyprland-protocols/blob/main/protocols/hyprland-lock-notify-v1.xml).

Live GDM introspection also exposes `CreateTransientDisplay`,
`CreateUserDisplay`, and `DestroyUserDisplay` on `LocalDisplayFactory`. None is
used here. `CreateUserDisplay` is an administrative per-user display API and is
not a replacement for the normal user chooser.
