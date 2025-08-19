import logging
import os
import time
import subprocess
import textwrap

from mpd import MPDClient
from systemd.journal import JournalHandler

MUSIC_DIR = os.path.expanduser("~/Music")
DEFAULT_COVER = "/usr/share/pixmaps/debian-logo.png"  # fallback image
BLACK_IMAGE = "/home/sim/black.jpg"  # pure black 7" screen-sized jpg
CAVA_CMD = [
    "rxvt",
    "+sb",
    "-bg",
    "black",
    "-fg",
    "white",
    "-e",
    "cava",
    "-p",
    os.path.expanduser("~/.config/cava/config"),
]
FEH_CMD = ["feh", "-F", "-Z", "--image-bg", "black"]

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


def start_cava():
    if not is_running("cava"):
        subprocess.Popen(CAVA_CMD)
        log_info("Started CAVA")
    else:
        log_info("CAVA already running")


# def start_feh(cover_path: str):
#     # 1) Put up black, 2) kill any feh, 3) start feh with cover
#     kill_prog("cava")
#     show_black()
#     kill_prog("feh")
#     time.sleep(0.05)
#     subprocess.Popen(FEH_CMD + [cover_path])
#     log_info("Started feh", COVER=cover_path)


# def get_cover_path(song):
#     if not song:
#         return DEFAULT_COVER
#     file_path = os.path.join(MUSIC_DIR, song.get("file", ""))
#     album_dir = os.path.dirname(file_path)
#     cover_path = os.path.join(album_dir, "cover.jpg")
#     if not os.path.isfile(cover_path):
#         return DEFAULT_COVER
#     return cover_path


def osd_message(text, delay=5, x=350, y=0, width=100):
    try:
        log_info( "Displaying OSD message: " + text, TEXT=text, X=x, Y=y, WIDTH=width) # fmt: skip
        p = subprocess.Popen(
            [
                "dzen2", "-ta", "c", 
                "-x", str(x), "-y", str(y),  "-w", str(width),
                "-fn", "DejaVu Sans-20", "-p", str(delay)
            ],
            stdin=subprocess.PIPE,
            text=True
        )  # fmt: skip
        p.stdin.write(text + "\n")
        p.stdin.close()
        # don’t wait — return immediately
    except FileNotFoundError:
        log_error("OSD: dzen2 not installed, skipping message")
    except Exception as e:
        log_error("OSD: Error displaying message: " + str(e))


# --- MAIN LOOP ---
def main():
    client = MPDClient()
    client.timeout = 10
    client.idletimeout = None
    client.connect("localhost", 6600)
    start_cava()

    last_state = None
    last_song = None
    last_title = None

    while True:
        try:
            status = client.status()
            state = status.get("state")
            song = client.currentsong()
            title = song.get("title")
            artist = song.get("artist")
            album = song.get("album", "")

            if song != last_song:
                log_info("Current song changed", LAST_SONG=last_song, SONG=song)
                last_song = song

            if title and (title != last_title):
                # First line: Artist - Title
                if artist:
                    line1 = f"{artist} - {title}"
                else:
                    line1 = title
                osd_message(line1, delay=4, x=0, y=0, width=800)

                # Second line: Album (only if present)
                if album:
                    osd_message(album, delay=4, x=0, y=30, width=800)

                last_title = title

            # Playback state change
            if state != last_state:
                log_info(
                    "Playback state changed: " + state,
                    LAST_STATE=last_state,
                    STATE=state,
                )
                match state:
                    case "play":
                        osd_message("▶ Play", delay=2, x=340, y=100, width=120)

                    case "pause":
                        osd_message("|| Pause", delay=2, x=340, y=100, width=120)

                    case "stop":
                        osd_message("◼ Stop", delay=2, x=340, y=100, width=120)

                    # case _:
                    # kill_prog("cava")

                last_state = state

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
