# Install on the Linux Mint server

Once the image is published, installation needs only the HDHomeRun IP address. This one command downloads the installer and starts CatchUp DVR:

```sh
curl -fsSL https://raw.githubusercontent.com/Hewbacca/CatchupDVR/main/deploy/install.sh | bash -s -- 192.168.1.50
```

The installer verifies Podman Compose, checks the tuner, selects the Intel `/dev/dri/renderD*` device, creates private configuration, pulls the image, starts the container, and waits for a successful health check.

The first GHCR publish is private by default. In GitHub, open the new `catchupdvr` package, choose **Package settings → Change visibility → Public** once. Public GHCR images can then be pulled by Podman without a login.

By default it stores everything under `./catchup-dvr`. To put recordings on a large disk:

```sh
curl -fsSL https://raw.githubusercontent.com/Hewbacca/CatchupDVR/main/deploy/install.sh | bash -s -- 192.168.1.50 /mnt/dvr-recordings
```

Open `http://LINUX_SERVER_IP:8095` from the iPad or Android phone. Install it from the browser's Share/Add to Home Screen menu.

If the repository is checked out locally, update later with:

```sh
./deploy/update.sh
```

Without a checkout, update with `cd catchup-dvr && podman compose pull && podman compose up -d`.

## Optional overrides

- `CATCHUP_IMAGE`: image and tag to pull. Default during development: `ghcr.io/hewbacca/catchupdvr:edge`.
- `CATCHUP_INSTALL_DIR`: directory for compose configuration and the database.
- `CATCHUP_PORT`: host-network HTTP port. Default: `8095`.
- `CATCHUP_DATA_DIR`: database and guide-cache directory.

The initial service trusts the home LAN. Do not forward its port from the router or expose it directly to the internet.
