# Strava

Wattson can upload a finished ride to Strava as a FIT activity. Every ride can also be downloaded as a FIT file (History, open a ride, Download FIT) and uploaded by hand at <https://www.strava.com/upload/select>; that always works, with or without the setup below.

Strava only lets an app upload for athletes who connected to it, so you need a Strava API application of your own. Wattson ships without one.

## 1. Create a Strava API application

1. Sign in at Strava and open <https://www.strava.com/settings/api>.
2. Fill in the form:
   - **Application Name** and **Category**: anything.
   - **Website**: your Wattson URL, for example `https://wattson.example.com`.
   - **Authorization Callback Domain**: the host of Wattson's public URL, **without scheme, path or port**: `wattson.example.com`, not `https://wattson.example.com:8080`. For a LAN address use the address alone (`192.168.1.20`) or the local name (`wattson.home`). Strava also accepts `localhost`.
3. Save, then note the **Client ID** (digits) and **Client Secret**.

Set **Settings, General, Public URL** in Wattson first. The redirect Wattson sends is `<public URL>/api/strava/callback`, and the callback domain Strava expects is that URL's host. Settings, Integrations and the Riders card both print the exact domain to enter. Behind a reverse proxy, make sure the public URL is what the browser uses; a mismatch is the most common setup error (see Troubleshooting).

## 2. Enter the credentials

There are two places, and a rider's own app wins when both exist.

- **Shared app** (admin): Settings, Integrations, Strava. Every rider can then connect to it.
- **Per rider**: Riders, the rider's Strava section, **Use my own Strava app**. Enter that rider's client ID and secret.

The client secret is stored encrypted (see Privacy) and is never shown again; the field only says whether one is set. Changing the shared app's client ID or secret disconnects every rider who is connected through it, and changing a rider's own credentials disconnects that rider, because the tokens belong to the old app. Riders with an app of their own are not affected by a change to the shared app. Reconnect afterwards from Riders.

### Capacity: one athlete per new app

A new Strava API application can connect **1 athlete** (the account that owns it) until Strava raises its limit. A second person who tries to connect is refused by Strava. You have two options:

- **Give each rider their own app** (Riders, Use my own Strava app). Each rider creates an app under their own Strava account, so each has capacity for themselves. This needs no approval.
- **Ask Strava for more capacity** for the shared app: submit the developer-program request form linked from <https://www.strava.com/settings/api> (see Strava's [Developer Program documentation](https://developers.strava.com/docs/getting-started/)). Approval is Strava's decision and can take time.

## 3. Connect a rider

1. Open **Riders**. Under the rider, the Strava section shows **Connect Strava** once an app is configured. Without an app it says Strava is not set up.
2. Select **Connect Strava**. Strava asks for permission. **Keep "Upload your activities" ticked** (scope `activity:write`); Wattson also asks for `read`. If you untick it Wattson refuses the connection and tells you to connect again.
3. You land back on Riders with "Strava connected." and "Connected as <your Strava name>".

Connecting needs a signed-in browser. A paired kiosk display cannot connect Strava ("Connect Strava from a signed-in browser"); do it from a phone or computer, then the display's rides upload like any other. The connect link works only for riders you own.

**Disconnect** (two taps) revokes Wattson's access at Strava (best effort) and forgets the tokens. It doesn't delete any activities. If Strava could not be reached, you can also remove the app under Strava, Settings, My Apps.

## 4. Uploading

- **By hand**: open the ride in History and select **Upload to Strava**. The button shows "Uploading…" while Strava processes the file (usually seconds; Wattson checks every 2 s for up to 2 minutes). When it finishes it becomes **View on Strava**. If the rider has not connected Strava, it says so and points to the Riders page. A failed upload shows the reason and a **Retry** button.
- **Automatically**: on a connected rider, switch on **Upload rides automatically** (off by default, per rider). Each ride is uploaded as it finishes. Rides shorter than about 10 seconds are not kept at all, so they are never uploaded. A rider who switches this on later does not trigger uploads of old rides; use the button for those.
- A ride is uploaded **once**. Pressing the button twice, or auto-upload racing a manual upload, still makes one Strava activity. If Strava reports the file as a duplicate of an existing activity, Wattson links that activity instead of failing. If the server restarts mid-upload, a ride that Strava had already accepted resumes checking; a ride interrupted before Strava accepted the file shows "interrupted; retry", unless the rider has auto-upload on, in which case Wattson sends it again on the next start.
- Deleting a ride in Wattson never deletes the Strava activity.

The activity is named after the route, or "Wattson ride" for a free ride, with the description "Recorded with Wattson", and is flagged as a trainer ride. Strava sets the type from the FIT file: route rides are written as virtual cycling, free rides as indoor cycling.

## Privacy

- **Credentials and tokens** (the client secret, access and refresh tokens) are encrypted with the server's `secret.key` before they are stored, never returned by the API and never logged. Back up `secret.key` with the database; without it they cannot be read and riders must reconnect.
- **What Strava receives**: only rides you upload (by hand or through auto-upload), as a FIT file with the time, power, cadence, speed, distance and heart rate samples, plus position and altitude for route rides (free rides have none), and the activity name and description above. Rides you do not upload never leave your server.
- **What Wattson receives from Strava**: the athlete's name and ID at connect time, and upload status afterwards. Wattson does not read your Strava activities.
- Authorization state in the connect flow is signed, bound to your session user and valid for 10 minutes.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| Strava shows "redirect_uri invalid" or "bad request" after Connect | The Authorization Callback Domain at Strava does not match the host in Wattson's public URL. Enter the host only (no `https://`, no port) exactly as Riders or Integrations prints it. If you changed the public URL or access it by another name, update the Strava app too. |
| "Strava was connected without upload permission…" | The "Upload your activities" box was unticked at Strava. Connect again and keep it ticked. |
| "The Strava sign-in expired. Try again." | The connect flow takes more than 10 minutes, or was opened in another account. Start again from Riders in the same browser session. |
| "Strava rejected the connection. Check the client ID and secret." | Wrong ID or secret, or a second athlete trying a new app that has capacity 1. Re-enter the credentials; use a per-rider app or request more capacity. |
| "Strava is not set up." | Neither a shared nor a per-rider app is configured. |
| "Strava rate limit reached; try again in 15 minutes" | Strava limits an app to 100 requests per 15 minutes (1000 per day). Wait, then use Retry. Each upload costs a few requests, so this normally only appears after many uploads at once or when several riders share one app. |
| "Strava authorization expired; reconnect Strava for this rider" | Access was revoked at Strava or the refresh token was rejected. Wattson drops the link; connect again from Riders. |
| "Strava isn't configured on this server — ask an admin" | The shared app was removed or its secret cleared while the rider had no app of their own. |
| "Still uploading. Retry to check again." / "Strava is still processing; check again later" | Strava took longer than Wattson waits. Retry sends the FIT again and relies on Strava's duplicate detection (the same external ID), so the ride then shows as done with the existing activity, not as a second one. |
| An activity exists on Strava but Wattson shows an error | Select Retry. If Strava says the file is a duplicate, Wattson links the existing activity. |
| A duplicate appears when you upload by hand too | A hand upload of the downloaded FIT is independent of Wattson's record. Upload either one way or the other. |
| Auto-upload does nothing | The rider must be connected with "Upload rides automatically" on, and the ride must reach the minimum length. The server log shows `strava auto-upload` warnings with the reason. |
