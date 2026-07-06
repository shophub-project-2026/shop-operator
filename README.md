# shop-operator

Kubernetes operator za upravljanje Shop resursima, Discord notifikacijama i blockchain wallet-ima.

## Pregled

Shop Operator je Kubernetes Operator koji omogućava:

- **Shop CRD**: deployment Shop aplikacije sa 2 ili 3 replike
- **DiscordChannel CRD**: operator kreira poseban Discord kanal + webhook po shopu (preko bot API-ja); alarmi tog shopa idu na njegov kanal
- **Wallet CRD**: upravljanje blockchain wallet adresama za primanje uplata

## Zahtevi

- Go 1.21+
- Kubernetes 1.24+
- Docker

## Brz početak

### Build

```bash
make build
```

### Testovi

```bash
# Unit testovi
make test

# Integracioni testovi
make test-integration
```

### Docker

```bash
# Build Docker slike
make docker-build

# Push Docker slike koristeći VERSION ili git tag
make docker-push
```

### Lint

```bash
make lint
```

## CRD-jevi

### Shop

```yaml
apiVersion: shop.devops.io/v1alpha1
kind: Shop
metadata:
  name: my-shop
spec:
  availability: high
  walletAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f42e0"
  database: standard
  image: devops/shop:v1.0.0
```

### DiscordChannel

Operator za svaki shop kreira poseban kanal na Discord serveru i webhook na
njemu, pa rutira alarme tog shopa (`shop=<ime>`) na taj kanal. Za to operatoru
treba bot token i guild (server) ID — vidi `DISCORD_BOT_TOKEN` / `DISCORD_GUILD_ID`
(helm `discord.guildId` + `discord.botToken.secretName`). Bot mora biti pozvan u
guild sa permisijama **Manage Channels** i **Manage Webhooks**.

```yaml
apiVersion: shop.devops.io/v1alpha1
kind: DiscordChannel
metadata:
  name: my-shop
spec:
  channelName: "my-shop"        # kanal se kreira kao #shop-my-shop
  # guildId: "..."              # opciono: override default guild-a operatora
  # webhookUrl: "https://..."   # opciono: override — koristi postojeći kanal
                                # umesto da ga operator kreira
```

Status nakon reconcile-a sadrži `channelId`, `webhookId` i razrešeni `webhookUrl`
koji per-shop `AlertmanagerConfig` koristi. Brisanje CR-a (finalizer) briše i
kreirani kanal sa servera.

### Wallet

Wallet CRD **kreira account na blockchain-u** (§3.1) na koji korisnici vrše
uplatu, ili adoptira postojeći:

- **Kreiranje** — kada je `spec.address` prazan, operator generiše secp256k1
  keypair, izvede EIP-55 adresu i sačuva ključeve u Secret `<ime>-keys`
  (`address`, `private-key`). Adresa se objavljuje u `status.address`.
  Ethereum account postoji na chain-u čim postoji keypair — može odmah da
  prima uplate (Sepolia testnet).
- **Adopcija** — kada je `spec.address` zadat, operator validira format i
  preuzima adresu u `status.address`. `ShopReconciler` automatski kreira po
  jedan Wallet za svaki Shop (adopcija `spec.walletAddress`).
- **Balans** — operator periodično čita `eth_getBalance` (JSON-RPC; endpoint
  preko env `ETH_RPC_URL`, default Sepolia publicnode) i upisuje ga u
  `status.balance`.

```yaml
# Kreiranje novog accounta (operator generiše keypair):
apiVersion: shop.devops.io/v1alpha1
kind: Wallet
metadata:
  name: my-wallet
spec:
  blockchain: ethereum
  network: sepolia
  currency: ETH
---
# Adopcija postojećeg accounta:
apiVersion: shop.devops.io/v1alpha1
kind: Wallet
metadata:
  name: merchant-wallet
spec:
  address: "0x742d35Cc6634C0532925a3b844Bc9e7595f42e00"
  blockchain: ethereum
  network: sepolia
  currency: ETH
```

## Napomena: Redis operator (umesto REDB)

Za `database: light` prodavnice koristi se open-source
[OT-Container-Kit redis-operator](https://github.com/OT-CONTAINER-KIT/redis-operator)
umesto REDB-a iz specifikacije: REDB je kontroler za **Redis Enterprise**
(komercijalni proizvod sa licencom). Specifikacija izričito dozvoljava drugu
bazu "ali je obavezno koristiti operator te baze" — Redis se ovde deployuje
isključivo kroz CRD Redis operatora, ekvivalentno CNPG-u za PostgreSQL.

## Integracioni testovi

`make test-integration` podiže **pravi k3s klaster kroz Testcontainers**
(§5.2), instalira CRD-ove i pokreće sve reconcilere:
standard→2 replike / high→3, Service/Ingress/dashboard/PrometheusRule/
DiscordChannel/Wallet po prodavnici, kreiranje wallet accounta (keypair
Secret) i garbage-collection pri brisanju Shop-a. Zahteva pokrenut Docker.

## Deployment

Vidi `../helm-charts` za Helm chart za deployment operatora.

## Razvojna sredstva

```bash
# Format kod
make fmt

# Vet
make vet

# Clean
make clean
```

## Konvencije koda

- Prosledi `context.Context` svim funkcijama koje rade sa Kubernetes API-jem
- Koristi `zap` logging
- Prati SOLID principe i Clean Architecture
- Prosledi interfejse, vraćaj strukte
- Reconciliation loop-e treba da budu idempotentne

## Licenca

MIT
