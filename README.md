# shop-operator

Kubernetes operator za upravljanje Shop resursima, Discord notifikacijama i blockchain wallet-ima.

## Pregled

Shop Operator je Kubernetes Operator koji omogućava:

- **Shop CRD**: deployment Shop aplikacije sa 2 ili 3 replike
- **DiscordChannel CRD**: konfiguraciju Discord webhook-a za notifikacije
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

```yaml
apiVersion: shop.devops.io/v1alpha1
kind: DiscordChannel
metadata:
  name: my-discord-channel
spec:
  webhookUrl: "https://discordapp.com/api/webhooks/..."
  channelName: "shop-notifications"
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
```

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
