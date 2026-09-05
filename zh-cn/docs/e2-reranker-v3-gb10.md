# GB10 reranker evidence v3

决策 0030 修正真实 vLLM 0.23 的日志参数，并记录隔离 GB10 部署所需的显存比例。
历史 v1/v2 provider 与中央 artifacts 保持原样。本工具独立于产品运行时，只能发送契约固定的合成输入。

provider 固定中央 commit `84be856` 和 conformance root
`64cf1bbcefbc9fec1ae238d382cf69ba28c1d2f5c545bb041f3b9f19725eae51/15492`，验证真实有序 argv、
decimal/binary64 显存比例、完整模型/tokenizer catalog 和原生模型/分数语义。

`scripts/e2_reranker_live_smoke_v3.py` 要求已评审真实 manifest 的 SHA/size、可信 CA、单独固定的
resolved cache configuration、新证据目录和持久 attempt ledger；只连接数字地址的 loopback HTTPS。
原生指标必须分别存在唯一、精确的 engine/model counter series，缺失、重复、非有限、小数或无法精确
表达的计数一律拒绝。缓存配置取自已评审的真实部署导出文件，不制造 gauge。

`scripts/e2_reranker_https_v3.py` 保留上游 body 原字节并添加两枚身份 header，每次转发前后核对实际
容器 identity，POST 仅允许精确合成正文与 request ID。collector 和入口均在发送前独占创建并 fsync
attempt record；中断或失败的 POST 不能用同一 ledger 重发。入口只用于本次隔离诊断部署，
不是通用公网模型网关。TLS 私钥留在 operator 私有 runtime 目录，评审证据只包含 CA 证书。

在 SparkClaw 根目录、且存在 sibling InfiniCenter 时执行离线检查：

```bash
python3 -m unittest scripts.test_e2_reranker_evidence_v3 scripts.test_e2_reranker_live_smoke_v3
```

fake-server suite 明确使用合成 fixture，不能替代真实部署评审。真实 smoke receipt 仍须经过外部
byte-pin review，之后才进入 reference parity、tolerance 或 calibration 工作。

2026-09-05 UTC，已评审的 GB10 部署完成唯一一次 planned native POST，返回 HTTP 200。
manifest `c08e03610a6da695b9b01cc70cb1b9db034aaf66a8087cb340f70e656984d8c4/12893` 与
receipt `a5818f01f8cf961c3ddc60a625b80dcacaf2e04a7e673d5dc7c0d23bfec97bca/9926`
通过独立 Go/Python 实现的外部 pin 检查。receipt 首次保存在 IMMS commit
`2aef26ba54346c0d7eebe42e5150155b11df317f` 的
`docs/evidence/gb10-e2-v3-20260905/smoke-stable/receipt.json`。两个 attempt ledger 已消耗，
本文不授权重新发送 POST。完整 scripts 验证 148 项通过。
[GB10 结果](../../../IMMS/docs/gb10-e2-admission-2026-09-05.md) 保留先前仅 GET 的 mount 换序拒绝、
精确部署审阅、镜像补丁候选及剩余 parity/calibration/生产工作。历史 v1/v2 保持原样。
