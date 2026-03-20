## Варианты установки

### Бинарник с GitHub

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')
curl -sL "https://github.com/lavr/express-botx/releases/latest/download/express-botx-${OS}-${ARCH}.tar.gz" | tar xz
sudo mv express-botx /usr/local/bin/
```

### Homebrew

Установка:
```bash
brew install lavr/tap/express-botx
```

Обновление:
```bash
brew upgrade lavr/tap/express-botx
```

### Docker

```bash
docker run -it --rm lavr/express-botx --version
```



### Go

Простая установка и сборка:

```bash
go install github.com/lavr/express-botx@latest
```

### Из исходников

```bash
git clone https://github.com/lavr/express-botx.git
cd express-botx

go build -tags "sentry newrelic rabbitmq kafka" -o express-botx .
```
