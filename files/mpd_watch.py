import logging
import os
import time
import subprocess
import textwrap
import threading

from evdev import InputDevice, categorize, ecodes
from mpd import MPDClient
from systemd.journal import JournalHandler

MUSIC_DIR = os.path.expanduser("~/Music")
DEFAULT_COVER = "/usr/share/pixmaps/debian-logo.png"  # fallback image
BLACK_IMAGE = "/home/sim/black.jpg"  # pure black 7" screen-sized jpg
LOG_FILE = os.path.expanduser("~/mpd_watch.log")
CAVA_CMD = ["cava", "-p", os.path.expanduser("~/.config/cava/config")]
FEH_CMD = ["feh", "--fullscreen", "--image-bg", "black"]

# Stable symlink for GPIO IR receiver
IR_DEVICE = "/dev/input/by-path/platform-ir-receiver@11-event"

# Configure logger
logger = logging.getLogger("mpd_watch")
handler = JournalHandler(SYSLOG_IDENTIFIER="mpd_watch")
handler.setFormatter(logging.Formatter("%(levelname)s: %(message)s"))
logger.addHandler(handler)
logger.setLevel(logging.INFO)


def log_info(msg, **kwargs):
    logger.info(msg, extra=kwargs)


def log_warn(msg, **kwargs):
    logger.warning(msg, extra=kwargs)


def log_error(msg, **kwargs):
    logger.error(msg, extra=kwargs)


if not os.path.exists(IR_DEVICE):
    logger.warning(
        f"WARNING: IR device {IR_DEVICE} not found at startup. "
        "Check udev rule /etc/udev/rules.d/99-ir-remote.rules"
    )


def is_running(name: str) -> bool:
    return (
        subprocess.call(
            ["pgrep", "-x", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
        )
        == 0
    )


def kill_prog(name):
    subprocess.run(
        ["pkill", "-x", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
    )


def show_black():
    # Brief black screen to mask switch
    # p = subprocess.Popen(FEH_CMD + [BLACK_IMAGE])
    time.sleep(0.25)  # let it paint a frame
    # return p  # not strictly used, but handy if you want to target-kill later


def start_cava():
    show_black()
    kill_prog("feh")
    time.sleep(0.05)
    if not is_running("cava"):
        subprocess.Popen(CAVA_CMD)
        log_info("Started CAVA")
    else:
        log_info("CAVA already running")


def start_feh(cover_path: str):
    # 1) Put up black, 2) kill any feh, 3) start feh with cover
    kill_prog("cava")
    show_black()
    kill_prog("feh")
    time.sleep(0.05)
    subprocess.Popen(FEH_CMD + [cover_path])
    log_info("Started feh", COVER=cover_path)


def get_cover_path(song):
    if not song:
        return DEFAULT_COVER
    file_path = os.path.join(MUSIC_DIR, song.get("file", ""))
    album_dir = os.path.dirname(file_path)
    cover_path = os.path.join(album_dir, "cover.jpg")
    if not os.path.isfile(cover_path):
        return DEFAULT_COVER
    return cover_path


def osd_message(text, delay=4):
    """Display a short on-screen message using osd_cat, without blocking."""
    try:
        p = subprocess.Popen(
            [
                "osd_cat",
                "--pos=top",
                "--align=left",
                "--color=white",
                "--shadow=1",
                f"--delay={delay}",
                "--font=-adobe-helvetica-medium-r-normal--34-240-100-100-p-176-iso8859-1",
            ],
            stdin=subprocess.PIPE,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        p.stdin.write(text.encode())
        p.stdin.close()
        # log(f"OSD: {text}")
    except FileNotFoundError:
        log_warn("OSD: osd_cat not installed, skipping message")
    except Exception as e:
        log_error("OSD: error displaying message", TEXT=text, ERROR=e)


def listen_ir(client, device_path):
    while True:
        try:
            dev = InputDevice(device_path)
            log_info("IR: listening", DEVICE=device_path)
            for event in dev.read_loop():
                if event.type == ecodes.EV_KEY and event.value == 1:  # 1 = key press
                    key = categorize(event).keycode
                    log_info("IR: received", KEY=key)

                    if key == "KEY_PLAY":
                        if client.status().get("state") == "play":
                            client.pause()
                        else:
                            client.play()

                    elif key == "KEY_PAUSE":
                        client.pause()

                    elif key == "KEY_NEXT":
                        client.next()

                    elif key == "KEY_PREVIOUS":
                        client.previous()

                    elif key == "KEY_STOP":
                        client.stop()

                    elif key == "KEY_FASTFORWARD":
                        client.seekcur("+10")  # jump forward 10s
                        # log("Seek forward 10s")
                        osd_message(">> +10s", delay=2)

                    elif key == "KEY_REWIND":
                        client.seekcur("-10")  # jump back 10s
                        # log("Seek back 10s")
                        osd_message("<< -10s", delay=2)

        except FileNotFoundError:
            log_error("IR: device not found, retrying in 5s", DEVICE=device_path)
            time.sleep(5)

        except OSError as e:
            log_error(
                "IR: error reading device, retrying in 5s", DEVICE=device_path, ERROR=e
            )
            time.sleep(5)


# --- MAIN LOOP ---
def main():
    client = MPDClient()
    client.timeout = 10
    client.idletimeout = None
    client.connect("localhost", 6600)

    threading.Thread(target=listen_ir, args=(client, IR_DEVICE), daemon=True).start()

    last_state = None
    last_album = None
    last_song = None
    last_cover_path = None
    last_title = None

    while True:
        try:
            status = client.status()
            state = status.get("state")
            song = client.currentsong()
            title = song.get("title")
            artist = song.get("artist")
            album = song.get("album", "")
            feh_needed = False

            if song != last_song:
                log_info("Current song changed", LAST_SONG=last_song, SONG=song)
                last_song = song

            if title and (title != last_title):
                # First line: Artist - Title (wrapped if very long)
                if artist:
                    line1 = f"{artist} - {title}"
                else:
                    line1 = title
                wrapped_line1 = "\n".join(textwrap.wrap(line1, width=50))

                # Second line: Album (only if present, unwrapped)
                line2 = f"{album}" if album else ""

                now_playing = (
                    wrapped_line1 if not line2 else f"{wrapped_line1}\n{line2}"
                )

                osd_message(f"Now Playing:\n{now_playing}", delay=8)
                # log(f"Now Playing OSD: {now_playing}")
                last_title = title

            # Playback state change
            if state != last_state:
                log_info("Playback state changed", LAST_STATE=last_state, STATE=state)
                match state:
                    case "play":
                        start_cava()
                        osd_message("> Play", delay=2)

                    case "pause":
                        osd_message("|| Pause", delay=2)
                        kill_prog("cava")
                        feh_needed = True
                        log_info("Cava stopped due to pause")

                    case "stop":
                        osd_message("[] Stop", delay=2)
                        kill_prog("cava")
                        feh_needed = True
                        log_info("Cava stopped due to stop")

                    case _:
                        kill_prog("cava")
                        feh_needed = True

                last_state = state

            # Album change when not playing
            if state != "play" and album != last_album:
                feh_needed = True
                last_album = album

            # Start/refresh feh if required
            if feh_needed:
                cover_path = get_cover_path(song)
                if cover_path != last_cover_path or not is_running("feh"):
                    start_feh(cover_path)
                    last_cover_path = cover_path
                else:
                    log_info("feh already showing correct cover; no restart")

        except Exception as e:
            log_error("Error", ERROR=e)
            try:
                client.ping()
            except Exception:
                try:
                    client.connect("localhost", 6600)
                    log_info("Reconnected to MPD")
                except Exception as e2:
                    log_error("Reconnect failed", ERROR=e2)
                    time.sleep(2)

        time.sleep(1)


if __name__ == "__main__":
    main()
