# Hybuntu Hyprland configuration

A deliberately boring Hyprland setup for Ubuntu 26.04. The aim is to get a
small, conventional Wayland desktop without turning Ubuntu into a
distro-within-a-distro.

If you want something more exciting, opinionated, and cutting-edge, check out
[Omarchy](https://omarchy.org/).

## Project goals

1. **Stay within Ubuntu 26.04.** Use Ubuntu's normal package management,
   filesystem layout, login manager, and system services instead of replacing
   the base operating system.
2. **Use the Ubuntu archive only.** Every package explicitly installed by this
   project is available from Ubuntu 26.04 Universe. The installer does not add
   PPAs or third-party repositories, download upstream binaries, or build
   packages from source. Package dependencies may naturally come from other
   official Ubuntu components such as Main.
3. **Prefer configuration over scripts.** Runtime behavior belongs in the
   files under `cfg/`. Hyprland launches the underlying tools directly. The
   only project Bash script is `install.sh`, whose job is limited to installing
   packages, preserving existing config, creating links, and initializing the
   machine-local monitor file.
4. **Keep Hyprland boring.** Favor familiar applications, predictable
   shortcuts, plain configuration, and components packaged by Ubuntu. Avoid
   plugin stacks, elaborate theme frameworks, custom background services, and
   clever glue unless they solve a real problem that configuration alone
   cannot solve.

This repository bootstraps the Hyprland session, not every desktop
application. Commands such as the terminal, browser, and file manager are
declared near the top of `cfg/hypr/hyprland.conf` and can be changed to match
applications already installed on the machine.

## Installation

Start with Ubuntu 26.04 and make sure the official Universe component is
enabled. No additional package repository is required.

Run the installer from any directory:

```bash
./install.sh
```

It installs the Hyprland ecosystem packages with `apt-get` and symlinks every
configuration file under `cfg/` into the matching path below `~/.config`.
Existing files are preserved in a timestamped `~/.config/hybuntu-backup-*`
directory before they are replaced.

Mako provides desktop notifications and is started with the Hyprland session.
Its default theme lives in `cfg/mako/config`. Use
`makoctl mode -t do-not-disturb` to toggle do-not-disturb mode.

Ubuntu 26.04's plain GDM Hyprland session does not activate
`graphical-session.target`, so Hyprland launches `hypridle`,
`hyprpolkitagent`, Hyprpaper, Waybar, SwayOSD, and Mako directly. This keeps
their lifetime tied to the compositor instead of partially reproducing a
systemd-managed session. The package-provided user units remain installed and
are not disabled; do not start them separately while using this configuration.

PipeWire is intentionally different: Ubuntu's packaged systemd user units
manage the audio stack independently of Hyprland. The installer declares the
`pipewire-audio` metapackage and Pavucontrol, a graphical mixer for application
and device volumes. Clicking Waybar's volume indicator opens Pavucontrol.

SwayOSD displays volume and screen-brightness changes made with the multimedia
keys. Its client performs the adjustment and the compositor starts its display
server with the session. Volume is capped at 100%, brightness is kept above
2%, and the OSD includes the resulting percentage. Playerctl handles the media playback
keys. Brightnessctl is also installed for inspecting and controlling backlight
devices from the command line.

Hyprpaper displays the bundled `cfg/hypr/wallpapers/rainier-panorama.webp` in
`contain` mode on every monitor. This fits the whole panorama to each output's
width without cropping or distorting it; unused height remains blank. The
installer links the image into `~/.config/hypr/wallpapers/` with the rest of
the Hypr configuration.

Screenshot shortcuts use Grim, Slurp, and Swappy:

- `Print` selects a region and opens it in Swappy.
- `Shift+Print` captures the full desktop and opens it in Swappy.

Use Swappy to annotate, copy, or save the resulting image.

Monitor configuration is machine-specific. On first install,
`cfg/hypr/monitors.conf.example` is copied to
`~/.config/hypr/monitors.conf` as a regular file. Later installer runs leave
that file unchanged. Edit the installed file to match the outputs reported by
`hyprctl monitors`.

To install or test only the configuration without running `apt-get`, use:

```bash
./install.sh --skip-packages
```

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
