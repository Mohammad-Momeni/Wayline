# Wayline for Android

Install the arm64-v8a APK on modern ARM phones or armeabi-v7a on 32-bit ARM phones. Both require Android 8.0+. The stable application ID is io.routeweave.app; upgrades retain existing profiles.

Use the Connection, Chain, Xrays and Logs navigation buttons. Advanced network options are collapsed on the Connection page. VPN profiles have explicit Rename and Save actions. The Xray library displays imported names and chain positions; Settings edits endpoints, credentials and all JSON options.

VPN app filtering supports all apps, selected apps only, or selected exclusions. Search the app picker by name or package; icons identify each app. An external local exit proxy must be selected in its hop settings, requires Android 10+, and is always routed through the VPN while that hop is active. Imported Xray cores do not need an external proxy app.

A rejected certificate offers Cancel or Proceed. Remember saves its exact fingerprint for this profile; leave it unchecked to approve only the current connection. Changing the gateway clears remembered fingerprints.

Auto reconnect is optional. Manual disconnect cancels retries. Use Logs to copy or clear connection activity.

See BUILDING.md for command-line builds and signing. Android Studio is not required.
