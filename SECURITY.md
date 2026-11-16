# Security

Do not post passwords, gateway addresses, certificate fingerprints, private Xray links or full profile exports in public issues. Use placeholders in reproduction steps.

Certificate validation is enabled. Rejected gateway certificates require independent fingerprint verification. Authentication and certificate failures stop automatic retries.

Profiles contain connection metadata. Windows uses Credential Manager for remembered VPN and proxy passwords. Android uses Android Keystore. Linux can use Secret Service. Imported Xray JSON can contain credentials and remains in local storage.

Wayline is a pre-release client. It needs device and network testing before deployment in a managed environment. Disconnecting removes its routes. There is no persistent kill switch.
