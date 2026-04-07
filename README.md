# Music Player system

## mpd-controller

Manages communication and control of Music Player Daemon (MPD) instances.
Provides functionality to send commands to MPD servers and handle playback control operations.

## Display

Original config using X had:

```bash
$ cat /boot/firmware/cmdline.txt
console=serial0,115200 console=tty1 root=PARTUUID=560eb58f-02 rootfstype=ext4 fsck.repair=yes rootwait cfg80211.ieee80211_regdom=GB video=DSI-1:800x480@60,rotate=180
```

The `rotate=180` causes flashing and geometry issues with SDL apps. So just remove and ignore the upsidedown console messages.
