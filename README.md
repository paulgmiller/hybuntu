# Ubuntu Sway configuration

A deliberately boring Sway setup for Ubuntu 26.04. The aim is to get a small,
conventional Wayland desktop while keeping the applications, shortcuts, and
behavior of this repository's Hyprland setup as close as Sway allows.

If you want something more exciting, opinionated, and cutting-edge, check out
[Omarchy](https://omarchy.org/).

## Project goals

1. **Stay within Ubuntu 26.04.** Use Ubuntu's normal package management,
   filesystem layout, login manager, and system services instead of replacing
   the base operating system.
2. **Use the Ubuntu archive only.** The installer does not add PPAs or
   third-party repositories, download upstream binaries, or build packages
   from source. Enable the official Universe component before installing.
3. **Prefer configuration over scripts.** Runtime behavior belongs in the
   files under `cfg/`. The only project Bash script is `install.sh`, whose job
   is limited to installing packages, preserving existing config, creating
   links, and initializing the machine-local output file.
4. **Keep Sway boring.** Favor familiar applications, predictable shortcuts,
   plain configuration, and components packaged by Ubuntu. Avoid plugin
   stacks, elaborate theme frameworks, and custom background services.

This repository bootstraps the Sway session, not every desktop application.
Commands such as the terminal, browser, and file manager are declared near the
top of `cfg/sway/config` and can be changed to match applications already
installed on the machine.

## Installation

Start with Ubuntu 26.04 and make sure the official Universe component is
enabled. No additional package repository is required.

Run the installer from any directory:

```bash
./install.sh
```

It installs Sway and the supporting desktop packages with `apt-get`, then
symlinks every configuration file under `cfg/` into the matching path below
`~/.config`. Existing files are preserved in a timestamped
`~/.config/ubuntu-sway-backup-*` directory before they are replaced.

Choose **Sway** from GDM's session chooser when logging in.

To install or test only the configuration without running `apt-get`, use:

```bash
./install.sh --skip-packages
```

## Desktop components

Waybar, Mako, SwayOSD, swayidle, swaylock, and the PolicyKit authentication
agent start with Sway. PipeWire remains managed by Ubuntu's packaged systemd
user units. The distro-provided snippets under `/etc/sway/config.d/` are also
included so D-Bus and systemd-activated services receive the Sway environment.
Use `makoctl mode -t do-not-disturb` to toggle notification suppression.

The wlr desktop portal provides screen capture and screen sharing. The GTK
portal supplies common desktop dialogs. Both are D-Bus activated after Sway
imports its environment.

SwayOSD displays volume and screen-brightness changes made with the multimedia
keys. Volume is capped at 100%, brightness is kept above 2%, and the OSD shows
the resulting percentage. Playerctl handles media playback keys. Clicking
Waybar's volume indicator opens Pavucontrol.

Sway's built-in background integration runs swaybg and displays the bundled
`cfg/sway/wallpapers/rainier-panorama.webp` in `fit` mode on every output. This
shows the whole panorama without cropping or distorting it; unused space is
filled with the same dark color used by the lock screen.

Screenshot shortcuts use Grim, Slurp, and Swappy:

- `Print` selects a region and opens it in Swappy.
- `Shift+Print` captures the full desktop and opens it in Swappy.

Use Swappy to annotate, copy, or save the resulting image.

## Shortcuts

The bindings intentionally follow the previous Hyprland configuration:

| Shortcut | Action |
| --- | --- |
| `Super+Return` | Open the terminal |
| `Super+Space` | Open Fuzzel |
| `Super+B` | Open the browser |
| `Super+E` | Open the file manager |
| `Super+W` or `Super+C` | Close the focused window |
| `Super+L` | Lock the session |
| `Super+M` | Confirm and exit Sway |
| `Super+V` | Toggle floating mode |
| `Super+J` | Toggle the next split direction |
| `Super+Arrow` | Move focus |
| `Super+1` through `Super+0` | Switch to workspace 1 through 10 |
| `Super+Shift+1` through `Super+Shift+0` | Move a window to workspace 1 through 10 |
| `Super+S` | Show or hide a scratchpad window |
| `Super+Shift+S` | Move the focused window to the scratchpad |
| `Super+mouse wheel` | Cycle through workspaces |
| `Super+left/right drag` | Move or resize a window |

A three-finger horizontal touchpad swipe changes workspaces, matching the
Hyprland gesture. The volume, microphone, brightness, and media keys retain
their previous behavior and continue to work while the screen is locked.

Sway has no equivalents for Hyprland's window animations, rounded corners,
blur, shadows, or pseudotile mode. The Sway config preserves the 5-pixel inner
and outer gaps, borderless windows, full opacity, focus-follows-mouse behavior,
automatic split-tree layout, and disabled natural scrolling. `Super+P` is left
unbound because presenting floating mode as pseudotiling would be misleading.

## Outputs

Output configuration is machine-specific. On first install,
`cfg/sway/outputs.conf.example` is copied to
`~/.config/sway/outputs.conf` as a regular file. Later installer runs leave
that file unchanged. Edit the installed file to match the output names and
modes reported by:

```bash
swaymsg -t get_outputs
```

The example mirrors the previous two-monitor Hyprland layout, including the
90-degree rotation on `HDMI-A-1`.

## Locking and switching users

Swayidle locks after 10 minutes and powers the outputs off five seconds later.
Activity or resume powers them back on. Logind lock, unlock, and suspend events
are handled as well, and swaylock is fully established before suspend
continues. Vanilla swaylock has no equivalent to hyprlock's grace period, so
the password requirement takes effect immediately when the session locks.

GDM's greeter runs on tty1 on the Ubuntu machine for which this configuration
was written. To switch users without ending the current session:

1. Press `Super+L` and wait for swaylock to appear on every output.
2. Press `Ctrl+Alt+F1` to open GDM's normal user chooser.
3. Select another user and authenticate.

The original Sway session remains locked on its existing VT. On keyboards
where the F-keys default to media controls, use `Ctrl+Alt+Fn+F1`. If GDM uses a
different VT on your machine, adjust this instruction accordingly. Unlike
hyprlock, swaylock cannot display a custom switch-user instruction on its lock
surface.
