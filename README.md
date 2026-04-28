# shop-operator

Kubernetes operator za upravljanje Shop resursima, Discord notifikacijama i crypto wallet-ima.

## Pregled

Shop Operator je Kubernetes Operator napravljen sa Kubebuilder-om koji omogućava:

- **Shop CRD**: Automatski deployment Shop aplikacija sa specificiranim brojem replika
- **DiscordChannel CRD**: Konfiguracija Discord webhook-a za notifikacije
- **Wallet CRD**: Upravljanje blockchain wallet adresama za primanje uplata

## Zahtevi

- Go 1.21+
- Kubernetes 1.24+
- Docker (za build slike)

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
# Build Docker sliku
make docker-build

# Push na DockerHub
make docker-push IMAGE_TAG=v1.0.0
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
  availability: high  # standard (2 replike) ili high (3 replike)
  walletAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f42e0"
  database: standard  # standard (PostgreSQL) ili light (Redis)
  image: devopsmilos/shop:v1.0.0
```

### DiscordChannel

```yaml
apiVersion: shop.devops.io/v1alpha1
kind: DiscordChannel
metadata:
  name: my-discord-channel
spec:
  webhookUrl: "https://discordapp.com/api/webhooks/..."
  channelName: "shop-notifications"
  shopRef:
    name: my-shop
```

### Wallet

```yaml
apiVersion: shop.devops.io/v1alpha1
kind: Wallet
metadata:
  name: my-wallet
spec:
  address: "0x742d35Cc6634C0532925a3b844Bc9e7595f42e0"
  blockchain: ethereum
  network: testnet
  currency: USDT
  shopRef:
    name: my-shop
```

## Deployment

Vidi `../helm-charts` za Helm Chart za deployment operatora.

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

- Prosledi `context.Context` svim funkcijama koje rade sa Kubernetes API-jem ili blokira
- Koristi `zap` logging
- Prati SOLID principle-e i Clean Architecture
- Prosledi interfejse, vraćaj strukte
- Idempotentne reconciliation loop-e

## Licenca

MIT
