#!/usr/bin/env bash

set -Eeuo pipefail

readonly REPO_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly CFG_DIR="${REPO_DIR}/cfg"
readonly CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}"
readonly MONITORS_TEMPLATE="${CFG_DIR}/hypr/monitors.conf.example"
readonly MONITORS_FILE="${CONFIG_DIR}/hypr/monitors.conf"
readonly BACKUP_DIR="${CONFIG_DIR}/hybuntu-backup-$(date +%Y%m%d-%H%M%S)-$$"

readonly -a PACKAGES=(
    hyprland
    hypridle
    hyprlock
    hyprpaper
    hyprpolkitagent
    xdg-desktop-portal-hyprland
    fuzzel
    waybar
    swayosd
    pipewire-audio
    pavucontrol
    brightnessctl
    playerctl
    mako-notifier
    grim
    slurp
    swappy
    wl-clipboard
)

install_packages=true
backup_created=false

usage() {
    cat <<EOF
Usage: $(basename "$0") [--skip-packages]

Install the Hyprland ecosystem and link this repository's cfg files into:
  ${CONFIG_DIR}

Options:
  --skip-packages  Only install the configuration (useful for testing)
  -h, --help       Show this help
EOF
}

while (($#)); do
    case "$1" in
        --skip-packages)
            install_packages=false
            ;;
        -h | --help)
            usage
            exit 0
            ;;
        *)
            printf 'Unknown option: %s\n\n' "$1" >&2
            usage >&2
            exit 2
            ;;
    esac
    shift
done

backup_path() {
    local destination=$1
    local relative=${destination#"${CONFIG_DIR}/"}
    local backup_destination="${BACKUP_DIR}/${relative}"

    mkdir -p -- "$(dirname -- "$backup_destination")"
    mv -- "$destination" "$backup_destination"
    backup_created=true
    printf 'Backed up %s -> %s\n' "$destination" "$backup_destination"
}

ensure_config_directory() {
    local source_directory=$1
    local relative=${source_directory#"${CFG_DIR}/"}
    local destination="${CONFIG_DIR}/${relative}"

    if [[ -L "$destination" || ( -e "$destination" && ! -d "$destination" ) ]]; then
        backup_path "$destination"
    fi

    mkdir -p -- "$destination"
}

link_config_file() {
    local source=$1
    local relative=${source#"${CFG_DIR}/"}
    local destination="${CONFIG_DIR}/${relative}"

    if [[ -L "$destination" ]] &&
        [[ "$(readlink -f -- "$destination")" == "$(readlink -f -- "$source")" ]]; then
        printf 'Already linked: %s\n' "$destination"
        return
    fi

    if [[ -e "$destination" || -L "$destination" ]]; then
        backup_path "$destination"
    fi

    ln -s -- "$source" "$destination"
    printf 'Linked %s -> %s\n' "$destination" "$source"
}

ensure_local_monitors() {
    local local_copy

    if [[ -L "$MONITORS_FILE" ]]; then
        local_copy=$(mktemp "${CONFIG_DIR}/hypr/.monitors.conf.XXXXXX")

        if [[ -e "$MONITORS_FILE" ]]; then
            cp --dereference -- "$MONITORS_FILE" "$local_copy"
        else
            cp -- "$MONITORS_TEMPLATE" "$local_copy"
        fi

        chmod 0644 "$local_copy"
        backup_path "$MONITORS_FILE"
        mv -- "$local_copy" "$MONITORS_FILE"
        printf 'Converted monitor config to a local file: %s\n' "$MONITORS_FILE"
    elif [[ -e "$MONITORS_FILE" && ! -f "$MONITORS_FILE" ]]; then
        backup_path "$MONITORS_FILE"
        cp -- "$MONITORS_TEMPLATE" "$MONITORS_FILE"
        printf 'Replaced invalid monitor config with a local file: %s\n' "$MONITORS_FILE"
    elif [[ ! -e "$MONITORS_FILE" ]]; then
        cp -- "$MONITORS_TEMPLATE" "$MONITORS_FILE"
        printf 'Created local monitor config: %s\n' "$MONITORS_FILE"
    else
        printf 'Preserved local monitor config: %s\n' "$MONITORS_FILE"
    fi
}

if [[ "$install_packages" == true ]]; then
    if ! command -v apt-get >/dev/null 2>&1; then
        printf 'This installer requires apt-get (Ubuntu or Debian).\n' >&2
        exit 1
    fi

    if ((EUID == 0)); then
        sudo_command=()
    else
        if ! command -v sudo >/dev/null 2>&1; then
            printf 'sudo is required to install packages.\n' >&2
            exit 1
        fi
        sudo_command=(sudo)
    fi

    printf 'Updating apt metadata...\n'
    "${sudo_command[@]}" apt-get update

    printf 'Installing Hyprland packages...\n'
    "${sudo_command[@]}" apt-get install -y "${PACKAGES[@]}"
fi

mkdir -p -- "$CONFIG_DIR"

# Create real destination directories. This is important for hypr/ because its
# machine-specific monitors.conf must not live behind a repository symlink.
while IFS= read -r -d '' source_directory; do
    ensure_config_directory "$source_directory"
done < <(find "$CFG_DIR" -mindepth 1 -type d -print0 | sort -z)

# Examples are copied explicitly where needed; all other files are linked.
while IFS= read -r -d '' source; do
    link_config_file "$source"
done < <(
    find "$CFG_DIR" -type f \
        ! -name '*.example' \
        ! -path "${CFG_DIR}/hypr/monitors.conf" \
        -print0 | sort -z
)

ensure_local_monitors

if [[ "$backup_created" == true ]]; then
    printf 'Previous config was backed up under: %s\n' "$BACKUP_DIR"
fi

printf 'Hybuntu installation complete.\n'
