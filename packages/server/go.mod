module vaporrmm/server

go 1.25.0

require (
	github.com/gofiber/fiber/v2 v2.52.13
	github.com/gofiber/websocket/v2 v2.2.1
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.12.3
	github.com/mattn/go-sqlite3 v1.14.37
	github.com/pquerna/otp v1.5.0
	github.com/prometheus/client_golang v1.23.2
	github.com/redis/go-redis/v9 v9.19.0
	github.com/stretchr/testify v1.11.1
	golang.org/x/crypto v0.49.0
	vaporrmm/models v0.0.0
)

require (
	github.com/alicebob/miniredis/v2 v2.37.0 // indirect
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coreos/go-oidc/v3 v3.18.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fasthttp/websocket v1.5.3 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/savsgio/gotils v0.0.0-20230208104028-c358bd845dee // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.51.0 // indirect
	github.com/valyala/tcplisten v1.0.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	google.golang.org/protobuf v1.36.8 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace vaporrmm/models => ../models
