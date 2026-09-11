# Run CatchUp DVR

Podman or Docker is a prerequisite. The image is `ghcr.io/hewbacca/catchupdvr:edge`.

## Compose

This directory contains a ready-to-run Compose file. From this directory:

```sh
podman compose pull
podman compose up -d
```

It stores the database in `./data` and recordings in `./recordings`. Change those two bind mounts in [compose.yml](compose.yml) before starting if the recordings belong on another disk.

## First run

Open `http://SERVER_IP:8095`. Create an account, enter the HDHomeRun IP address or retain `hdhomerun.local`, and use **Test connection** before finishing. CatchUp stores the account and tuner address in `./data/catchup.db`.

## Updates

From this directory, run:

```sh
podman compose pull
podman compose up -d
```

For internet access, keep CatchUp behind a TLS reverse proxy such as Caddy. Do not forward its port from the router or expose it directly to the internet.
