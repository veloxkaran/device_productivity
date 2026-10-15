# Signing the desktop app & installers

Unsigned builds work, but trigger first-run security prompts:
- **macOS** re-asks for Screen Recording on every rebuild, and Gatekeeper warns.
- **Windows** SmartScreen warns ("unknown publisher").

## macOS — local development (stop the Screen-Recording re-prompt)

Creates a stable local code-signing certificate so the installed app keeps its
macOS permission grants across rebuilds. One-time:

```bash
make dev-setup          # creates "Hajir Local Signing" cert + clears stale grants
make publish-local      # (or publish-staging / publish-prod) rebuilds signed
```

Grant Screen Recording once after installing; it now sticks, because the
installer preserves the app's signature (ditto) instead of re-signing.

This is self-signed: good for your own machines, NOT for distribution (other
Macs still warn). For that, use a Developer ID below.

## macOS — production (no warnings on any Mac)

Requires an Apple Developer account. Set these, then build:

```bash
export SIGN_IDENTITY="Developer ID Application: Velox Labs (TEAMID)"
# notarization — either a stored notarytool profile:
export AC_NOTARY_PROFILE="hajir-notary"
#   (one-time: xcrun notarytool store-credentials hajir-notary \
#      --apple-id you@apple.com --team-id TEAMID --password APP_SPECIFIC_PW)
# …or raw creds: APPLE_ID + APPLE_TEAM_ID + APPLE_APP_PASSWORD

make publish-prod
```

`SIGN_IDENTITY` signs the app bundle (build-dmg.sh) and the GUI Install/Uninstall
apps (MAC_SIGN). build-dmg.sh notarizes the .dmg automatically when creds are
set. To notarize the GUI zip too:

```bash
xcrun notarytool submit dist/MyMonitor-GUI-Setup-macos.zip --keychain-profile hajir-notary --wait
```

## Windows — code signing (stops SmartScreen)

Uses `sign-windows.sh` (osslsigncode, cross-platform). No-op when unset, so
builds still work unsigned. Put these in `.env.production` (NOT in git):

```bash
export WIN_SIGN_PFX=/path/to/authenticode-cert.pfx
export WIN_SIGN_PASS='cert-password'
# install the signer once: brew install osslsigncode  (or apt-get install osslsigncode)

make publish-prod       # each Install/Uninstall .exe is signed before zipping
```

A code-signing cert from a CA (DigiCert, Sectigo, …) is required; an EV cert
clears SmartScreen reputation immediately.
