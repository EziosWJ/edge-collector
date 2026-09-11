# SQLite ARM64 测试部署记录

## 环境

- 目标：`root@192.168.0.232`
- 架构：`aarch64`
- 系统 libc：glibc 2.29
- 测试目录：`/userdata/base-go-api-test`
- API 端口：`8099`

## 编译

目标机没有 Go 和 GCC。使用 Zig 生成静态 ARM64 musl 二进制：

```sh
GOCACHE=/tmp/base-go-api-gocache \
ZIG_GLOBAL_CACHE_DIR=/tmp/zig-cache \
CC='/tmp/zig-x86_64-linux-0.15.2/zig cc -target aarch64-linux-musl' \
CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
go build -trimpath \
  -ldflags='-s -w -linkmode external -extldflags "-static"' \
  -o /tmp/base-go-api-arm64-cross/api-musl ./cmd/api
```

## 部署与启动

上传 `api-musl`，并将基础配置放在目标目录的 `configs/config.yaml`：

```sh
scp api-musl root@192.168.0.232:/userdata/base-go-api-test/api.new
```

启动参数：

```sh
cd /userdata/base-go-api-test
mv api.new api
nohup env \
  APP_ENV=prod \
  APP_DATABASE__DRIVER=sqlite \
  APP_DATABASE__URL=/userdata/base-go-api-test/data/base-go-api.db \
  APP_FILE__STORAGE_ROOT=/userdata/base-go-api-test/uploads \
  APP_JWT__SECRET='<测试密钥>' \
  ./api >api.log 2>&1 < /dev/null &
```

## 前端部署

使用远端 API 地址构建：

```sh
VITE_API_BASE_URL=http://192.168.0.232:8099 task frontend:build
```

将 `react-admin/dist` 部署到 `/userdata/base-go-api-test/web`，使用独立的
lighttpd 实例提供静态文件：

- 地址：`http://192.168.0.232:18080`
- 配置：`/userdata/base-go-api-test/lighttpd.conf`
- PID：`/userdata/base-go-api-test/web.pid`（当前为 `19419`）
- `/`、`/login` 和 JS/CSS 资源均返回 HTTP 200。
- `url.rewrite-if-not-file` 回退到 `index.html`，支持 React Router 刷新。
- 未修改模板机原有的 `80/18000` 服务。

## 结果

- 静态 ARM64 二进制启动成功。
- `http://192.168.0.232:8099/health` 返回 HTTP 200。
- `/ready` 返回 HTTP 200。
- SQLite 文件已创建：`/userdata/base-go-api-test/data/base-go-api.db`。
- 本地 SQLite migration 版本：schema `7`、seed `3`；备份已通过完整性校验后上传远端。
- 远端使用上传数据库启动成功，`admin / admin123` 登录验证通过。

## 注意

直接使用 glibc 交叉编译的二进制依赖 `GLIBC_2.32` 至 `GLIBC_2.34`，在该设备上无法启动；必须使用静态 musl 编译。
