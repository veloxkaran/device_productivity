# My Monitor — Android

Mobile agent for the Device Activity hub. It talks to the same hub API as the
desktop `my-monitor` app (device token → heartbeat, time entries, activity
samples, screenshots), so a phone appears as its own device on
`/employer/dashboard/activity`.

**Automatic-only, background-first.** There is no manual Start/Stop and no
sign-in screen. Setup is one step — paste the employer's **device token** — after
which the app:

* asks once for Android screen-capture permission, then starts tracking;
* **hides its launcher icon** (gone from the app drawer);
* **runs in the foreground service** in the background and keeps sending
  screenshots + active/idle time to the hub;
* **restarts on boot** and after app updates;
* follows the employer's interval / quality / max-width settings and skips
  screenshots while the screen is off.

The screen is reused from the desktop tracker (`web/templates/app.html`), so the
setup/tracking UI matches the desktop app. It's only visible during the one-time
token setup (and the one-tap resume on non-managed phones — see below).

## Two Android limits (OS-enforced, not code)

These cannot be removed by app code on a normal phone:

1. **Screen capture needs one tap per session.** Android shows the system
   "Start recording?" dialog, and after a reboot or a low-memory kill it is
   required again. Until it's granted, the app waits in the background and shows
   a single "Tap to resume" notification; the device still reports online (idle)
   on the dashboard.
2. **A notification is visible while capturing.** A screen-capturing foreground
   service must show an ongoing notification. It carries no Stop control.

### Fully-managed (MDM) company devices

On a company-owned device enrolled in an MDM (Intune, Scalefusion, Esper,
Android Enterprise, …), both limits can be removed by **MDM policy**, not by this
app:

* Grant `MediaProjection` silently: set the app as a *projection consent*
  exception / use the managed-device screen-capture policy so the system dialog
  is skipped. The app's auto-resume then re-captures on boot with no tap.
* Auto-grant `POST_NOTIFICATIONS` and the runtime permissions, and add the app
  to the battery-optimisation allow-list so the OS doesn't kill the service.
* Push the device token via managed configuration (or keep pasting it once).

The exact policy names differ per MDM; configure them against package
`com.veloxlab.hajirtracker`. On personal/unmanaged phones the two limits above
stay.

## Build

Needs the Android SDK (Android Studio) and JDK 17 on the Mac. Copies the APK into
`hub-data/downloads` so it shows on the dashboard's **Download app** tab.

```sh
cd device_productivity
make package-android HAJIR_API_URL=http://<mac-LAN-IP>:8001/api/v2 HUB_URL=http://<mac-LAN-IP>:4010
```

Use addresses the phone can reach (`localhost` on a phone is the phone itself).
For a Play-style signed build, add `mobile/android/keystore.properties`
(`storeFile`, `storePassword`, `keyAlias`, `keyPassword`); otherwise the debug
key is used.

`_moved_from_hajir/` and `_old/` hold earlier attempts — safe to delete.
