# Container Watch

轻量、可移植的 Docker 主机容器监控面板：查看当前/已停止容器、端口、镜像标签与 OCI 版本元信息；手动检查远端 registry 摘要；对发现更新的 Compose 服务提供显式手动更新。

## 功能

- 每 10 秒刷新容器列表及运行状态、端口映射、Compose 项目/服务名。
- 显示镜像标签、OCI version/revision、构建时间和本地 RepoDigest（镜像若未提供 OCI 标签则以 tag 显示）。
- 点击“检查镜像更新”后通过 Docker Registry HTTP API 检查匿名可读镜像的远端摘要；对比本机 RepoDigest。当前不支持给私有 registry 配置凭据，私有镜像会提示无法检查。
- 仅在确认有新镜像且容器由 Docker Compose 管理时显示“手动更新”。二次确认后执行 `docker compose pull <service>` 和 `docker compose up -d --no-deps <service>`，不会自动更新其他服务。
- Basic Auth、强密码要求、跨站请求拦截、systemd sandbox、无需持久化业务数据库。
- 适用于 Linux x86_64 与 ARM64；服务本身为 host-native Go 二进制，通过 Docker socket只读查询容器，手动更新使用本机 Docker Compose CLI。

## 安全边界

Docker socket 的访问权限基本等同宿主机 root。该服务因此以 root 运行，以便访问 Docker socket 和执行 Compose；不要把面板裸露到公网。首次安装默认监听 `0.0.0.0:8744`，请只在可信 LAN/防火墙内访问，或放在带 HTTPS 与额外访问控制的反向代理之后。`PANEL_PASSWORD` 自动随机生成并保存在 `/etc/container-watch.env`（权限 600）。不要把此密码或该文件放进工单、截图或公共仓库。

手动更新会拉取 tag 当前指向的镜像并重建对应 Compose 服务，期间服务会短暂中断；更新前请确保重要数据已备份且在持久化卷/绑定目录中。被设置为 `latest` 等可变 tag 的服务会跟随上游变化。非 Compose 容器仅能查看与检查，不提供自动重建，避免根据容器 inspect 信息猜测其启动参数。

## 安装（FNOS 或任意 Linux 主机）

要求：Docker Engine、Docker Compose v2；首次安装还需 Go 1.23+，或有可用的 Docker BuildKit（脚本会用临时 Go builder 镜像编译）；若项目 `dist/` 已含匹配架构的预编译二进制则直接安装。支持 `x86_64` 和 `aarch64`。

```bash
# 将项目目录放到服务器，例如 /vol2/docker/container-watch
cd /vol2/docker/container-watch
sudo bash ./install.sh
```

首次安装会输出一次性管理员账号和随机密码，请立即保存在密码管理器。浏览器打开 `http://<服务器局域网IP>:8744/`。

修改绑定地址、端口或凭据：

```bash
sudoedit /etc/container-watch.env
sudo systemctl restart container-watch
sudo systemctl status container-watch
sudo journalctl -u container-watch -f
```

可设置 `LISTEN_ADDR=192.168.1.20:8744` 将服务限制到某一网卡；默认 `0.0.0.0:8744`。`DOCKER_HOST` 默认使用 `/var/run/docker.sock`，可按需指定 Docker Engine endpoint；如 socket 在非标准路径，设置 `DOCKER_HOST=unix:///path/to/docker.sock`。修改 env 后重启服务。

卸载：

```bash
sudo systemctl disable --now container-watch
sudo rm -f /etc/systemd/system/container-watch.service /opt/container-watch/container-watch
sudo systemctl daemon-reload
```

卸载命令保留 `/etc/container-watch.env`，如确认不再需要，再自行删除。

## API

- `GET /health`：版本与健康状态（不含敏感信息）
- `GET /api/summary`：容器统计
- `GET /api/containers`：容器与镜像信息
- `POST /api/updates/check`：检查本地镜像 tag 对应的远端 digest
- `POST /api/containers/{id}/update`：Compose 服务拉取并重建（需要 Basic Auth + `X-Requested-With: ContainerWatch` 请求头）

管理 API 使用 HTTP Basic Auth，与网页使用同一凭据。
