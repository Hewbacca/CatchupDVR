# Real-server validation

The development Mac is not evidence that tuner, VA-API, or sustained chase play works on the Linux server. Validate these steps on the Linux Mint host before calling the MVP complete.

1. Confirm the HDHomeRun is reachable and note its current device data:

   ```sh
   curl -fsS http://HDHOMERUN_IP/discover.json
   curl -fsS http://HDHOMERUN_IP/lineup.json
   ```

2. Confirm the Intel render node and driver:

   ```sh
   ls -l /dev/dri
   vainfo --display drm --device /dev/dri/renderD128
   podman run --rm --device /dev/dri:/dev/dri catchup-dvr vainfo --display drm --device /dev/dri/renderD128
   ```

3. Set `HDHOMERUN_IP` and, only if auto-detection is wrong, `GPU_RENDER_DEVICE`. Start the service and check `/api/diagnostics`.

4. Refresh the guide twice and verify logs show a discovery request before each XMLTV request. Temporarily break WAN access and verify the previous guide remains visible.

5. Record two overlapping channels. Verify both start and a third padded overlap is shown as a conflict.

6. Run the chase-play acceptance test with a sports broadcast or long synthetic source:
   - wait at least 20 minutes after recording begins;
   - start from the beginning on an iPad and Android phone;
   - pause for two minutes;
   - use +30/+60 repeatedly and -10 at least five times;
   - press Go Live and confirm playback reaches the advancing edge;
   - repeat after rotating the device and backgrounding/foregrounding the PWA;
   - stop the service mid-recording, restart it, and confirm the playable prefix is retained;
   - let a normal recording finish and confirm `#EXT-X-ENDLIST` is present.

7. AirPlay from iPad Safari and confirm audio/video remain synchronized after seeking.

