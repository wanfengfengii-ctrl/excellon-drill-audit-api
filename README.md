# Drill Statistics API — 受限 Excellon 钻孔程序校验与统计

纯后端 HTTP 服务。加工厂的 CAM 工程师在把 PCB 钻孔程序（Excellon/NC Drill）
交付产线前，可用本服务在上传环节做一次严格校验：**任何一行不合格，整份文件
拒绝（HTTP 422），绝不返回部分统计**；文件完全合法时返回每个已使用刀具的
孔数、总孔数以及所有孔心 X/Y 的最小、最大坐标。

- 语言/框架：Go 1.25 + [Gin](https://github.com/gin-gonic/gin)
- 数值运算：[shopspring/decimal](https://github.com/shopspring/decimal)（精确十进制，无浮点误差）
- 测试：[testify](https://github.com/stretchr/testify)
- 部署：多阶段 Docker 镜像（distroless 运行时）+ Docker Compose（API 常驻服务 + 一次性 verify 服务）

## 目录结构

```
.
├── cmd/
│   ├── api/           # HTTP 服务入口
│   ├── verify/        # 一次性端到端冒烟检查（Compose 的 verify 服务）
│   └── healthcheck/   # 容器健康检查小程序（distroless 内无 shell/curl）
├── internal/
│   ├── excellon/      # 词法/结构校验与统计（核心逻辑，含完整单测）
│   └── api/           # Gin 路由与 HTTP 适配（含单测）
├── Dockerfile
├── docker-compose.yml
└── go.mod
```

## API

### `POST /drill-files/statistics`

提交一份钻孔程序文本并返回统计结果。

- 请求：`Content-Type: text/plain`（允许 `text/plain; charset=utf-8` 等参数），请求体为 UTF-8 纯文本
- 成功：`200 OK`，JSON 响应
- 文件内容不合法：`422 Unprocessable Entity`，JSON 错误体
- 非 `text/plain`：`415 Unsupported Media Type`
- 请求体上限 10 MiB，超出返回 `413`

成功响应示例：

```json
{
  "tools": [
    { "tool": "T01", "holes": 2 },
    { "tool": "T02", "holes": 1 }
  ],
  "total_holes": 3,
  "min_x": "-0.500",
  "min_y": "-3.250",
  "max_x": "10.000",
  "max_y": "2.000"
}
```

- `tools` 只包含**实际钻孔过的刀具**，按刀具在头部的**定义顺序**排列；只定义未使用的刀具不出现
- 所有坐标统一输出为**三位小数字符串**（`decimal.StringFixed(3)`），不带科学计数法，保留负号

错误响应示例：

```json
{ "code": "UNDEFINED_TOOL", "line": 5 }
```

| code | 含义 |
|---|---|
| `LINE_ORDER` | 结构/指令错误：行序不对、出现不允许的指令、空行、缺行、CRLF、多余坐标轴等 |
| `INVALID_NUMBER` | 数值词法错误：前导零、小数位不是 1–3 位、直径非正数、坐标负零、指数写法等 |
| `DUPLICATE_TOOL` | 头部重复定义同一刀号 |
| `UNDEFINED_TOOL` | 正文选择了头部未定义的刀具（或未选刀就钻孔） |
| `NO_HOLES` | 文件结构完整但正文没有任何钻孔记录 |

### `GET /healthz`

存活探针，固定返回 `200 ok`。

## 受限 Excellon 格式

每行一条语句，**不接受空行**，不接受 CRLF（只按 LF 分行，单个结尾 LF 视为末行终止符）。

```
M48                 # 第 1 行必须是 M48
METRIC              # 第 2 行固定为 METRIC
TnnC直径            # 一行或多行刀具定义；nn 为 01..99（00 非法）；直径必须 > 0
%                   # 单独一行结束头部（之前至少要有一条刀具定义）
Tnn                 # 正文：选择一把已定义刀具
X坐标Y坐标          # 正文：钻孔行，X、Y 必须同时出现且各出现一次，顺序为 X 在前 Y 在后
M30                 # 最后一行必须是 M30
```

正文只允许刀具选择行与钻孔行（可任意交错；未选刀就钻孔按 `UNDEFINED_TOOL` 处理），
其余任何指令（`G00`、`M03`、`INCH`、注释等）一律拒绝。

数值（直径与坐标）的规范十进制写法：

- 整数部分为 `0`，或**无前导零**的正整数（`0.5`、`12` 合法；`00`、`01.5` 非法）
- 可带小数点，小数位 **1–3 位**（`1.`、`.5`、`1.1234` 非法；不带小数点也合法）
- 负号只允许出现在**坐标**上，且数值必须非零（`-0`、`-0.000` 非法；直径永远不允许负号）
- 直径除词法外还要求数值严格大于零（`T01C0.000` → `INVALID_NUMBER`）

### 错误选择规则（确定性）

1. **逐行校验，只报告文本中最早的错误行**；后面的行不再检查，因此绝不会泄露部分统计。
2. 同一行内按固定优先级取错误码：**行序结构 → 数值词法 → 重复定义/未定义引用**。
   - 例如 `T01C0.0` 与既有 T01 重复：直径为零（`INVALID_NUMBER`）优先于 `DUPLICATE_TOOL`
   - 例如未选刀就遇到 `X01Y1`：`01` 词法非法（`INVALID_NUMBER`）优先于未选刀（`UNDEFINED_TOOL`）
3. `M30` 不是最后一行时，错误定位在该 `M30` 行；文件在 `M30` 前结束时定位在“缺失的下一行”。
4. 无任何钻孔记录时，在 `M30` 所在行报 `NO_HOLES`。

## 本地开发

需要 Go 1.25。

```bash
go test ./...              # 全部单元测试
go run ./cmd/api           # 默认监听 :8080
API_PORT=9090 go run ./cmd/api

# 对运行中的服务执行一次端到端检查
API_URL=http://localhost:9090 go run ./cmd/verify
```

调用示例：

```bash
printf 'M48\nMETRIC\nT01C0.300\n%%\nT01\nX1.500Y2.250\nX-0.500Y2.250\nM30\n' \
  | curl -s -X POST localhost:8080/drill-files/statistics \
    -H 'Content-Type: text/plain' --data-binary @-
```

## Docker Compose

```bash
docker compose up --build           # 启动 API（宿主 8080）并运行一次性 verify
docker compose up -d api            # 只在后台启动 API
API_PORT=9090 docker compose up -d  # 用 API_PORT 覆盖宿主端口
docker compose logs verify          # 查看一次性检查结果（通过后退出 0）
```

- `api` 服务：常驻，内置容器健康检查（自带 `/healthcheck` 探针，适配 distroless）
- `verify` 服务：等待 `api` 健康后启动，跑一组成功/失败用例（成功体、三类 422、
  最早错误、415 等），输出 `verify: all checks passed` 后退出；任一断言失败则退出码非 0

镜像为多阶段构建：`golang:1.25-bookworm` 编译三个静态二进制，运行时为
`gcr.io/distroless/static-debian12`（非 root 用户，无 shell）。
