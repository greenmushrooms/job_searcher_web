# Hetzner box & WireGuard VPN

Non-secret reference for the deploy target. **Secrets (the WireGuard private key)
live in the gitignored `.env`**, keyed as below — they are never committed here.

## Host

| | |
|---|---|
| Hostname | `hoodatlas.thegoreliks.ca` |
| Public IP | `62.238.11.206` |
| Role | Hetzner box — intended deploy target / container hub |

> Not to be confused with `vpn.thegoreliks.ca` (192.168.4.x), which is the *home*
> WireGuard server on this workstation, unrelated to Hetzner.

## VPN

A [gluetun](https://github.com/qdm12/gluetun) container in **custom WireGuard**
mode tunnels to the box. This is the config from the "briefly funneled my traffic
through it" experiment, recovered from
`/mnt/ContainerHub/DockerConfig/vpn-test/docker-compose.yaml` (the only surviving
copy — it was never committed and the aggregator now runs on PIA instead).

Topology (non-secret):

| Setting | `.env` key | Value |
|---|---|---|
| Endpoint IP | `WIREGUARD_ENDPOINT_IP` | `62.238.11.206` |
| Endpoint port | `WIREGUARD_ENDPOINT_PORT` | `51820` |
| Client tunnel address | `WIREGUARD_ADDRESSES` | `10.10.0.20/32` |
| Server (peer) public key | `WIREGUARD_PUBLIC_KEY` | `ZU4C0p/hEIJwgM5seC0vbqlWt7GTxjyNFiu25rSmYw4=` |
| Client private key | `WIREGUARD_PRIVATE_KEY` | *(secret — in `.env` only)* |
| Firewall input ports | `FIREWALL_VPN_INPUT_PORTS` | `1234,3334` |
| Firewall outbound subnets | `FIREWALL_OUTBOUND_SUBNETS` | `192.168.1.0/24,172.16.0.0/12` |

### Bring up the tunnel

A gluetun service driven entirely from these `.env` vars:

```yaml
services:
  gluetun:
    image: qmcgaw/gluetun
    container_name: gluetun-hetzner
    cap_add: [NET_ADMIN]
    devices: ["/dev/net/tun:/dev/net/tun"]
    env_file: .env
    environment:
      - VPN_SERVICE_PROVIDER=custom
      - VPN_TYPE=wireguard
      - TZ=America/Toronto
    restart: "no"
```

(`gluetun` reads `WIREGUARD_*` and `FIREWALL_*` straight from the environment.)
