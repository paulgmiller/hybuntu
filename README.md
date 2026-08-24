# Hybuntu Hyprland configuration

## Switching users

GDM's greeter runs on tty1, so switching users does not need a custom helper:

1. Press `Super+L` to start hyprlock.
2. Wait until the lock screen is visible on every monitor.
3. Press `Ctrl+Alt+F1` to open GDM's normal user chooser.
4. Select another user and authenticate.

The original Hyprland session remains locked and running on its existing VT.
Selecting that user again in GDM resumes the existing session instead of
terminating it.

The hyprlock screen displays the `Ctrl + Alt + F1` instruction below the
password field. On keyboards where the F-keys default to media controls, use
`Ctrl+Alt+Fn+F1`.

This setup assumes GDM uses tty1, as it does on the Ubuntu 26.04 machine for
which this configuration was written. User sessions have appeared on tty2 and
tty3. If the greeter is configured on another VT, update both the instruction
in `cfg/hypr/hyprlock.conf` and this README.

Relevant files:

- `cfg/hypr/hyprland.conf` binds `Super+L` directly to `hyprlock`.
- `cfg/hypr/hyprlock.conf` contains the on-screen switch-user instruction.
- `cfg/hypr/hypridle.conf` retains the existing idle, suspend, and logind lock
  handling.

There is no custom switch-user executable or D-Bus integration.
