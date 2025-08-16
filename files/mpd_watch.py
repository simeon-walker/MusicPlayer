import os
import time
import subprocess
import threading

from evdev import InputDevice, categorize, ecodes
from mpd import MPDClient

MUSIC_DIR = os.path.expanduser("~/Music")
DEFAULT_COVER = "/usr/share/pixmaps/debian-logo.png"  # fallback image
BLACK_IMAGE = "/home/sim/black.jpg"      # pure black 7" screen-sized jpg
LOG_FILE = os.path.expanduser("~/mpd_watch.log")
CAVA_CMD = ["cava", "-p", os.path.expanduser("~/.config/cava/config")]
FEH_CMD = ["feh", "--fullscreen", "--image-bg", "black"]

# Stable symlink for GPIO IR receiver
IR_DEVICE = "/dev/input/by-path/platform-ir-receiver@11-event"

def log(msg):
    with open(LOG_FILE, "a") as f:
        f.write(f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] {msg}\n")

if not os.path.exists(IR_DEVICE):
    log(f"WARNING: IR device {IR_DEVICE} not found at startup. "
        "Check udev rule /etc/udev/rules.d/99-ir-remote.rules")

def is_running(name: str) -> bool:
    return subprocess.call(["pgrep", "-x", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL) == 0

def kill_prog(name):
    subprocess.run(["pkill", "-x", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

def show_black():
    # Brief black screen to mask switch
    p = subprocess.Popen(FEH_CMD + [BLACK_IMAGE])
    time.sleep(0.25)  # let it paint a frame
    return p  # not strictly used, but handy if you want to target-kill later

def start_cava():
    show_black()
    kill_prog("feh")
    time.sleep(0.05)
    if not is_running("cava"):
        subprocess.Popen(CAVA_CMD)
        log("Started CAVA")
    else:
        log("CAVA already running")

def start_feh(cover_path: str):
    # 1) Put up black, 2) kill any feh, 3) start feh with cover
    kill_prog("cava")
    show_black()
    kill_prog("feh")
    time.sleep(0.05)
    subprocess.Popen(FEH_CMD + [cover_path])
    log(f"Started feh with {cover_path}")

def get_cover_path(song):
    if not song:
        return DEFAULT_COVER
    file_path = os.path.join(MUSIC_DIR, song.get("file", ""))
    album_dir = os.path.dirname(file_path)
    cover_path = os.path.join(album_dir, "cover.jpg")
    if not os.path.isfile(cover_path):
        return DEFAULT_COVER
    return cover_path

def osd_message(text, delay=2):
    try:
        subprocess.Popen([
            "osd_cat", "--pos=bottom", "--align=center",
            "--color=white", "--shadow=1", f"--delay={delay}",
            "--font=-misc-fixed-bold-r-normal--20-200-75-75-c-100-iso8859-1"
        ], stdin=subprocess.PIPE).communicate(input=text.encode())
        log(f"OSD: {text}")
    except FileNotFoundError:
        log("OSD: osd_cat not installed, skipping message")

def listen_ir(client, device_path):
    while True:
        try:
            dev = InputDevice(device_path)
            log(f"IR: listening on {device_path}")
            for event in dev.read_loop():
                if event.type == ecodes.EV_KEY and event.value == 1:  # 1 = key press
                    key = categorize(event).keycode
                    log(f"IR: received {key}")
                    
                    if key == "KEY_PLAY":
                        if client.status().get("state") == "play":
                            client.pause()
                            osd_message("Playback paused")
                            log("Playback paused")
                        else:
                            client.play()
                            osd_message("Playback started")
                            log("Playback started")

                    elif key == "KEY_PAUSE":
                        client.pause()
                        osd_message("Playback paused")
                        log("Playback paused")

                    elif key == "KEY_NEXT":
                        client.next()
                        osd_message("Next track")
                        log("Skipped to next track")

                    elif key == "KEY_PREVIOUS":
                        client.previous()
                        osd_message("Previous track")
                        log("Skipped to previous track")

                    elif key == "KEY_STOP":
                        client.stop()
                        osd_message("Playback stopped")
                        log("Playback stopped")

                    elif key == "KEY_FASTFORWARD":
                        client.seekcur("+10")  # jump forward 10s
                        osd_message(">> +10s")
                        log("Seek forward 10s")

                    elif key == "KEY_REWIND":
                        client.seekcur("-10")  # jump back 10s
                        osd_message("<< -10s")
                        log("Seek back 10s")

                    elif key == "KEY_MENU":  # example button for toggle
                        global override_visualizer
                        override_visualizer = not override_visualizer
                        log(f"Toggled visualizer override → {override_visualizer}")
            
        except FileNotFoundError:
            log(f"IR: device {device_path} not found, retrying in 5s")
            time.sleep(5)
        except OSError as e:
            log(f"IR: error reading {device_path}: {e}, retrying in 5s")
            time.sleep(5)

# --- MAIN LOOP ---
def main():
    client = MPDClient()
    client.timeout = 10
    client.idletimeout = None
    client.connect("localhost", 6600)

    threading.Thread(target=listen_ir, args=(client,IR_DEVICE), daemon=True).start()

    last_state = None
    last_album = None
    last_song = None
    last_cover_path = None
    override_visualizer = False

    while True:
        try:
            status = client.status()
            state = status.get("state")
            song = client.currentsong()
            album = song.get("album", "")
            feh_needed = False

            if song != last_song:
                log(f"Current song changed: {last_song} -> {song}")
                last_song = song    
            
            # Playback state change
            if state != last_state:
                log(f"Playback state changed: {last_state} -> {state}")
                if state == "play" and not override_visualizer:
                    start_cava()
                else:
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
                    log("feh already showing correct cover; no restart")

        except Exception as e:
            log(f"Error: {e}")
            try:
                client.ping()
            except Exception:
                try:
                    client.connect("localhost", 6600)
                    log("Reconnected to MPD")
                except Exception as e2:
                    log(f"Reconnect failed: {e2}")
                    time.sleep(2)


        time.sleep(1)

if __name__ == "__main__":
    main()