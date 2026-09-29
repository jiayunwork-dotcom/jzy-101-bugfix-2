# 矩形波导模式核算服务

射频实验室工具：把常用矩形波导截面登记成具名**参数档案**，工程师点名档案并给出
工作频率与模式指数，服务一次性返回截止频率、传播状态、波导波长/衰减常数等结论，
无需每次重新敲尺寸。也支持不登记档案、直接提交截面参数临时现算。

- 语言/框架：Go 1.22 + Gin
- 存储：进程内并发安全 map（`sync.RWMutex`），无需外部数据库
- 端口：固定 **8080**
- 内置样例档案：**WR-90**（X 波段标准波导，22.86 mm × 10.16 mm，空气填充），
  主模 TE10 截止频率约 6.557 GHz，在 8.2–12.4 GHz 内为单模传播

## 物理模型

矩形波导 TE/TM(m,n) 模截止频率：

```
fc = c / (2·√(εr·μr)) · √((m/a)² + (n/b)²)
```

其中 `a` 宽边、`b` 窄边、`εr` 相对介电常数、`μr` 相对磁导率。

| 条件 | 状态 | 返回字段 |
|---|---|---|
| `f > fc` | `propagating` 传导 | `phase_constant` β、`guide_wavelength` λg |
| `f = fc`（1e-9 相对容差内） | `critical` 临界 | β=0、α=0；**无**波导波长（物理上 λg→∞，不硬凑数值，也不返回 Infinity） |
| `f < fc` | `evanescent` 渐逝 | `attenuation_constant` α（Np/m）；**无**波导波长 |

- 波导波长：`λg = 2π/β = λ/√(1-(fc/f)²)`，f 从截止点升高时单调下降并趋近介质波长 λ。
- 衰减常数：`α = (2πfc/v)·√(1-(f/fc)²)`（采用 fc 形式，避免近截止点的浮点抵消）。
- `(0,0)` 模式物理上不存在，作为非法模式拒绝。
- 校验规则：`a > b > 0`，`εr ≥ 1`、`μr ≥ 1`，频率为正有限值；
  所有校验在任何计算发生之前完成，错误逐项给出字段与原因。

## 模块划分

```
cmd/server/main.go          程序入口（固定 :8080）
internal/domain/types.go    共享领域类型
internal/physics/           截止频率、β/λg/α、传播状态判定（纯函数核心公式）
internal/validate/          几何、模式、频率、档案名的输入校验
internal/store/             并发安全进程内存档（含 WR-90 内置档案）
internal/service/           业务编排 + 批量调度（两条计算路径共用同一 dispatch）
internal/api/               Gin 接口层，只做绑定与错误映射，不含公式
```

## 构建与运行

```bash
make build && ./bin/waveguide-service        # 本地
make docker-build && docker run --rm -p 8080:8080 waveguide-service
```

容器内运行自动化测试（含 `-race`）：

```bash
make docker-test
# 或：docker build --target test -t wg-test . && docker run --rm wg-test
```

本地：`make test` / `make test-race`。

## HTTP 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 健康检查 |
| POST | `/api/v1/profiles` | 登记新档案（重名返回 409，不覆盖） |
| GET | `/api/v1/profiles` | 列出全部档案 |
| GET | `/api/v1/profiles/:name` | 查看单个档案（不存在 404） |
| DELETE | `/api/v1/profiles/:name` | 删除档案（不存在 404） |
| POST | `/api/v1/profiles/:name/calculate` | 点名档案 + 模式 + 频率（支持批量） |
| POST | `/api/v1/calculate` | 临时提交截面参数现算（与上一条同一套核算逻辑） |

尺寸单位为米，频率单位为 Hz；`rel_permittivity`/`rel_permeability` 缺省为 1。

### 示例

登记档案：

```bash
curl -s localhost:8080/api/v1/profiles -d '{
  "name": "WR-90-copy",
  "broad_dimension": 0.02286,
  "narrow_dimension": 0.01016
}'
```

点名 WR-90，TE10，批量求三个频率（10 GHz 传导 / 5 GHz 渐逝 / 恰为截止频率的临界；
精确临界值可直接取档案响应中的 `dominant_cutoff_frequency`）：

```bash
curl -s localhost:8080/api/v1/profiles/WR-90/calculate -d '{
  "mode": {"m": 1, "n": 0},
  "frequencies": [10e9, 5e9, 6557140376.202975]
}'
```

响应中每个频率点独立给出 `ok/result/error`：非法频率点只标记该点失败，
不拖累同批其它点。`result.state` 为 `propagating` 时带 `guide_wavelength`；
为 `evanescent` 时只带 `attenuation_constant`；为 `critical` 时 β=α=0 且
`guide_wavelength` 字段缺省。

临时提交（含 εr=4 介质，截止频率相对空气精确减半）：

```bash
curl -s localhost:8080/api/v1/calculate -d '{
  "broad_dimension": 0.02286,
  "narrow_dimension": 0.01016,
  "rel_permittivity": 4,
  "mode": {"m": 1, "n": 0},
  "frequency": 6e9
}'
```

## 自动化测试覆盖的关键关系

- 宽边放大两倍，主模截止频率**精确**减半（逐位相等）
- εr 从 1 换成 4，截止频率**精确**减半
- 高阶模式截止频率不低于主模
- `f == fc` 判定为临界：非渐逝、无（有限或 Infinity 的）波导波长，β=α=0
- λg 随 f 升高单调下降、恒大于介质波长并在高频极限逼近它
- 渐逝状态只有衰减常数、不出现波导波长
- 两条计算路径结果逐字段一致
- 批量中单个非法频率点不影响其它点
- 重名登记 409、删除/查询不存在档案 404、(0,0) 模式与越界几何 400
- 并发存储与并发 HTTP 请求（配合 `-race`）
