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
│   ├── excellon/      # 词法/结构校验、统计与拼板步进重复核对（核心逻辑，含完整单测）
│   └── api/           # Gin 路由与 HTTP 适配（含单测）
├── Dockerfile
├── docker-compose.yml
└── go.mod
```

## API

### `POST /drill-files/statistics`

提交一份钻孔程序文本并返回统计结果。

- 请求：`Content-Type: text/plain`（允许 `text/plain; charset=utf-8` 等参数），请求体为 UTF-8 纯文本
- 可选查询参数 `min_clearance`：规范十进制且 ≥ 0（同文件内数值写法，不允许负号）。传入后启用
  **孔边最小间距审计**：任意两孔的中心距必须 ≥ 两孔半径之和 + `min_clearance`，否则整份文件拒绝；
  恰好相切（等于）视为通过。比较全程使用距离平方与精确十进制运算，无开方、无浮点误差。
  参数非法（如 `-1`、`1.1234`、`01`）直接返回 `400 INVALID_CLEARANCE`，不进入文件解析；
  未传参时行为与响应与之前完全一致
- 可选且**不可重复**的查询参数 `symmetry_center=x,y`：x、y 各为一个文件坐标词法的规范十进制
  （允许负号与零），如 `symmetry_center=1.5,-2`。传入后在文件通过语法、数值、刀具引用及
  `min_clearance` 审计之后启用**半周（180°）旋转对称审计**：以刀号与精确规范化坐标为键，
  每个孔必须有同刀号孔位于旋转点 `2×center−point`；恰好位于中心的孔是半周旋转的**不动点**
  （`2×center−point == point`），旋转后仍是自身，因此任意数量（含单个）都计数守恒、直接通过。
  任一非不动点旋转键数量不足即整份文件拒绝，返回 `422 ASYMMETRIC_PATTERN`，并按正文行序在
  `uncovered_lines` 中给出配对后仍无对应孔的源行（`line` 为其中第一行）。参数重复或非法
  （如 `1`、`0,01`、`-0,0`）直接返回 `400 INVALID_SYMMETRY`，在读取正文之前拒绝；
  未传参时响应与之前完全一致
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

间距冲突的错误体会额外携带 `conflict_line`（与当前行孔位冲突的较早孔所在行）：

```json
{ "code": "HOLE_CLEARANCE", "line": 7, "conflict_line": 6 }
```

旋转失衡的错误体会额外携带 `uncovered_lines`（按正文行序消耗可配对计数后，仍找不到同刀号
旋转孔的源行；`line` 为其中第一行）：

```json
{ "code": "ASYMMETRIC_PATTERN", "line": 10, "uncovered_lines": [10] }
```

| code | 含义 |
|---|---|
| `LINE_ORDER` | 结构/指令错误：行序不对、出现不允许的指令、空行、缺行、CRLF、多余坐标轴等 |
| `INVALID_NUMBER` | 数值词法错误：前导零、小数位不是 1–3 位、直径非正数、坐标负零、指数写法等 |
| `DUPLICATE_TOOL` | 头部重复定义同一刀号 |
| `UNDEFINED_TOOL` | 正文选择了头部未定义的刀具（或未选刀就钻孔） |
| `NO_HOLES` | 文件结构完整但正文没有任何钻孔记录 |
| `HOLE_CLEARANCE` | 仅在传入 `min_clearance` 时出现：当前行孔位与 `conflict_line` 的较早孔位间距不足 |
| `ASYMMETRIC_PATTERN` | 仅在传入 `symmetry_center` 时出现：孔图形关于该中心不保持半周旋转对称，`uncovered_lines` 为无旋转对应的源行 |

间距审计只在行结构、数值词法、刀具引用全部合法之后执行，既有错误优先级不变；
`min_clearance` 参数本身的非法值返回 `400 INVALID_CLEARANCE`（客户端错误），不会被当作文件错误。
旋转对称审计在整份文件通过语法、数值、刀具引用与间距审计之后才执行，因此既有文件错误与间距错误
始终先于对称审计；`symmetry_center` 参数本身的重复或非法值返回 `400 INVALID_SYMMETRY`
（客户端错误），在读取正文之前拒绝，不会被当作文件错误。

### `POST /drill-files/panel-audit`

核对步进重复（step-and-repeat）生成的拼板是否完整：上传**模板**（单个拼板单元的钻孔程序）与
**拼板**（整板钻孔程序）两份受限 Excellon 文件，服务依次按 0°、90°、180°、270°（逆时针）旋转
模板，检验拼板是否恰好由同一方向的若干模板实例平铺而成。

- 请求：`Content-Type: multipart/form-data`，两个文件字段 **`template`** 与 **`panel`**（缺任一个
  返回 `400 MISSING_PART` 并指明 `part`）；每个部分上限 10 MiB，超出返回 `413`
- 两份文件复用与统计入口完全相同的校验；**先判 template 再判 panel**。任一文件非法时返回
  `422`，错误体在原错误码与行号之外携带 **`part`**（`"template"` 或 `"panel"`）指明出错文件
- 匹配规则（每个旋转方向独立、确定性执行）：
  1. 取旋转后模板中按（直径、X、Y）数值序最小的孔为**锚**；
  2. 反复从**剩余拼板孔**的同序最小项推导偏移量（拼板最小孔 − 模板锚），再按模板行序把整份
     模板从“（直径、变换坐标）多重集”中逐孔扣减；
  3. 多重集只按**直径与坐标**计数：重复孔、同直径异刀号的孔、实例间重叠都按份数守恒处理；
  4. 某方向扣减失败（拼板孔多余或缺失）时记录证据：锚点**拼板行**、首个缺失**模板行**与
     当前偏移量；拼板被恰好耗尽时该方向成功
- 成功：`200 OK`，按角度列出**全部**可行布局；每个布局给出所有实例的偏移量与锚点坐标，
  实例按锚点坐标排序
- 四个方向全部失败：`422 PANEL_PATTERN_MISMATCH`，`failures` 按角度顺序携带四条失败证据
- 非 `multipart/form-data`：`415 Unsupported Media Type`

成功响应示例：

```json
{
  "layouts": [
    {
      "angle": 0,
      "instances": [
        { "offset_x": "10.000", "offset_y": "5.000", "anchor_x": "10.000", "anchor_y": "5.000" },
        { "offset_x": "20.000", "offset_y": "3.000", "anchor_x": "20.000", "anchor_y": "3.000" }
      ]
    }
  ]
}
```

失败响应示例（拼板多一个孔，四个方向均无法恰好耗尽）：

```json
{
  "code": "PANEL_PATTERN_MISMATCH",
  "failures": [
    { "angle": 0, "anchor_line": 8, "missing_line": 7, "offset_x": "99.000", "offset_y": "99.000" },
    { "angle": 90, "anchor_line": 6, "missing_line": 7, "offset_x": "10.000", "offset_y": "5.000" },
    { "angle": 180, "anchor_line": 8, "missing_line": 6, "offset_x": "101.000", "offset_y": "99.000" },
    { "angle": 270, "anchor_line": 6, "missing_line": 6, "offset_x": "10.000", "offset_y": "7.000" }
  ]
}
```

文件非法的错误体示例（拼板文件第 5 行选择了未定义刀具）：

```json
{ "code": "UNDEFINED_TOOL", "line": 5, "part": "panel" }
```

| code | 含义 |
|---|---|
| `MISSING_PART` | 缺少 `template` 或 `panel` 文件字段（400），`part` 指明缺失字段 |
| `PANEL_PATTERN_MISMATCH` | 四个旋转方向都无法把拼板恰好耗尽；`failures` 逐角度给出锚点拼板行、首个缺失模板行与偏移量 |

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
5. 启用 `min_clearance` 审计时，间距检查排在同一行的结构、数值、刀具引用检查**之后**；
   每个通过校验的孔按正文行序与此前所有孔比较，首次冲突即在当前行报 `HOLE_CLEARANCE`
   并给出冲突的较早行 `conflict_line`。
6. 启用 `symmetry_center` 审计时，对称检查在整份文件（含间距审计）全部通过**之后**才执行：
   领域层以规范化三位小数坐标与刀号为键，把每个非不动点孔与其旋转键 `2×center−point`
   的最早可用同刀号孔消耗配对；恰好位于中心的孔是不动点，旋转后即自身，单个或任意多个都
   直接通过、也不会被当作其他孔的伙伴。配对后仍剩余的源行按正文行序收集为
   `uncovered_lines`，首行作为 `line`，报 `ASYMMETRIC_PATTERN`。重复孔可互换消耗，因此结果
   与正文配对顺序无关、确定可复现；旋转点上若只有**不同刀号**的孔则不构成配对。

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
  最早错误、415、`min_clearance` 合法/相切/冲突/非法参数，以及 `symmetry_center`
  精确对称成功、数量失衡、自映射、非法/重复参数、审计优先级，拼板审计的多实例成功、
  旋转识别、重叠重复孔守恒、多余/缺失孔的四方向失败证据、文件错误的 `part` 归属与
  媒体类型拒绝等），输出
  `verify: all checks passed` 后退出；任一断言失败则退出码非 0

镜像只由 `api` 服务声明一次构建；`verify` 复用同一个本地镜像（换用 `/verify`
入口，`pull_policy: never`），避免两个服务并行构建并争用 `drillapi:latest`
同名标签。若只想跑验收，先构建再启动即可：`docker compose build api && docker compose up verify`。

镜像为多阶段构建：`golang:1.25-bookworm` 编译三个静态二进制，运行时为
`gcr.io/distroless/static-debian12`（非 root 用户，无 shell）。
