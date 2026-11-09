My Tesla for Garmin Venu 4
==========================

A Connect IQ watch app that controls a Tesla through [Tessie](https://tessie.com).

- **Home screen:** a battery ring around the edge (green while charging), the percentage, range and
  cabin temperature, and four quick-action buttons along the bottom:

  | Icon                     | Tap                   | Press and hold          |
  |--------------------------|-----------------------|-------------------------|
  | Padlock                  | Lock                  | Unlock                  |
  | Fan                      | Turn climate on / off | Same as tap             |
  | Car, front lid filled in | Shows a hint          | Open the frunk          |
  | Car, rear lid filled in  | Shows a hint          | Open or close the trunk |

  Anything that opens the car needs a press and hold, so brushing the screen can't open it.
  The button for a running command shows a spinner, and buttons light up when something
  needs attention: orange when unlocked or open, blue while climate is running. The line at the
  bottom says what a tapped button is doing, or how long ago the status was updated.
- **More:** tap anywhere else on the screen, or press the top button, for honk, flash lights and refresh.
- **Glance:** battery bar, range and lock state from the last time you opened the app.

Distance and temperature follow the units set in the car. Requests go from the watch through the
Garmin Connect app on your phone to Tessie, so the phone needs to be nearby with Bluetooth on.

## Build

1. Install the [Connect IQ SDK Manager](https://developer.garmin.com/connect-iq/sdk/), sign in,
   and download the latest SDK plus the **Venu 4 41mm** / **Venu 4 45mm** devices.
2. Get an API token from the Tessie web dashboard (Settings > API).
3. Build on macOS or Linux:

   ```sh
   TESSIE_TOKEN=your-token ./build.sh            # Venu 4 45mm
   TESSIE_TOKEN=your-token ./build.sh venu441mm  # Venu 4 41mm
   ```

   The first run creates `developer_key.der`, your signing key. Keep it, because builds signed with
   a different key won't update the app in place. Set `TESSIE_VIN` too if you have several cars;
   otherwise the app uses the first active car on your Tessie account.

The token is baked into the build because Garmin Connect only offers app settings for apps
installed from the Connect IQ store. `build.sh` writes it into a temporary copy, never into this
folder. Don't share the built `.prg` file, because anyone with it can control your car.

On Windows, run the same `monkeyc` command that `build.sh` runs from the SDK's `bin` folder,
after putting your token into `resources/settings/properties.xml` (and not committing it).

## Try it in the simulator

```sh
connectiq &                                       # starts the simulator
monkeydo bin/MyTesla-venu445mm.prg venu445mm
```

The simulator sends real requests to Tessie, so the buttons control the real car.

## Install on the watch

1. Plug the watch into your computer with its USB cable. On a Mac, use
   [OpenMTP](https://openmtp.ganeshrvel.com/) to see its files.
2. Copy `bin/MyTesla-venu445mm.prg` into the watch's `GARMIN/APPS` folder.
3. Unplug the watch. **My Tesla** appears in the apps list, and you can add its glance from the glance settings.

To change the token later, rebuild and copy the new file over the old one.
