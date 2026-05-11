# nlink E2E 集成测试

双节点 docker-compose 冒烟测试：

- `nlink-server`：监听 7000（控制通道）+ 18080（Dashboard）+ 19999（代理 remote_port）
- `echo`：本地 TCP echo 后端（socat + /bin/cat），监听 9999
- `nlink-client`：连接 server，注册代理 `echo:9999 → server:19999`

## 运行

```bash
./deploy/e2e/run.sh
```

脚本会：

1. `docker compose up -d --build` 构建并启动三个容器；
2. 轮询 `http://127.0.0.1:18080/health` 直到就绪；
3. 等待 `127.0.0.1:19999` 监听（代理注册完成）；
4. 通过 `nc` 向 19999 发送字符串，验证 echo 往返；
5. 成功退出 0，失败输出最后 200 行日志并退出非 0；
6. 退出时自动 `docker compose down -v`。

## 注意

- VPN 功能需要 TUN 设备，默认配置关闭了 VPN。要测试 VPN 请在 compose 中添加 `cap_add: [NET_ADMIN]` + `devices: [/dev/net/tun]`。
- 若本机已有服务占用 7000/18080/19999，请先停止或在 compose 中改映射端口。
