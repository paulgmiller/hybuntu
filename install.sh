#!/usr/bin/env bash

set -Eeuo pipefail

readonly REPO_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly CFG_DIR="${REPO_DIR}/cfg"
readonly CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}"
readonly OUTPUTS_TEMPLATE="${CFG_DIR}/sway/outputs.conf.example"
readonly OUTPUTS_FILE="${CONFIG_DIR}/sway/outputs.conf"
readonly BACKUP_DIR="${CONFIG_DIR}/ubuntu-sway-backup-$(date +%Y%m%d-%H%M%S)-$$"

readonly -a PACKAGES=(
    sway
    swaybg
    swayidle
    swaylock
    xwayland
    policykit-1-gnome
    xdg-desktop-portal-wlr
    xdg-desktop-portal-gtk
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

Install Sway and its desktop components, then link this repository's cfg files
into:
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

ensure_local_outputs() {
    local local_copy

    if [[ -L "$OUTPUTS_FILE" ]]; then
        local_copy=$(mktemp "${CONFIG_DIR}/sway/.outputs.conf.XXXXXX")

        if [[ -e "$OUTPUTS_FILE" ]]; then
            cp --dereference -- "$OUTPUTS_FILE" "$local_copy"
        else
            cp -- "$OUTPUTS_TEMPLATE" "$local_copy"
        fi

        chmod 0644 "$local_copy"
        backup_path "$OUTPUTS_FILE"
        mv -- "$local_copy" "$OUTPUTS_FILE"
        printf 'Converted output config to a local file: %s\n' "$OUTPUTS_FILE"
    elif [[ -e "$OUTPUTS_FILE" && ! -f "$OUTPUTS_FILE" ]]; then
        backup_path "$OUTPUTS_FILE"
        cp -- "$OUTPUTS_TEMPLATE" "$OUTPUTS_FILE"
        printf 'Replaced invalid output config with a local file: %s\n' "$OUTPUTS_FILE"
    elif [[ ! -e "$OUTPUTS_FILE" ]]; then
        cp -- "$OUTPUTS_TEMPLATE" "$OUTPUTS_FILE"
        printf 'Created local output config: %s\n' "$OUTPUTS_FILE"
    else
        printf 'Preserved local output config: %s\n' "$OUTPUTS_FILE"
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

    printf 'Installing Sway desktop packages...\n'
    "${sudo_command[@]}" apt-get install -y "${PACKAGES[@]}"
fi

mkdir -p -- "$CONFIG_DIR"

# Create real destination directories. This is important for sway/ because its
# machine-specific outputs.conf must not live behind a repository symlink.
while IFS= read -r -d '' source_directory; do
    ensure_config_directory "$source_directory"
done < <(find "$CFG_DIR" -mindepth 1 -type d -print0 | sort -z)

# Examples are copied explicitly where needed; all other files are linked.
while IFS= read -r -d '' source; do
    link_config_file "$source"
done < <(
    find "$CFG_DIR" -type f \
        ! -name '*.example' \
        ! -path "${CFG_DIR}/sway/outputs.conf" \
        -print0 | sort -z
)

ensure_local_outputs

if [[ "$backup_created" == true ]]; then
    printf 'Previous config was backed up under: %s\n' "$BACKUP_DIR"
fi

printf 'Sway installation complete.\n'
