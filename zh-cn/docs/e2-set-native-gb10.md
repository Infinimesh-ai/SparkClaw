# GB10 native set reranker 诊断

决策0031/0032新增独立、显式的`imms-set-native-v1` profile。固定vLLM0.23默认全文分词与
IMMS要求的边界不同，并让正文控制标记进入控制ID。`e2_set_tokenizer_v1.py`保持六段精确顺序，
只从私有不可信backend移除26个added-token matcher，继续要求精确roundtrip、长度和零控制ID。
`e2_set_processor_v1.py`核对模型/template/request与最终引擎IDs；patcher只接受固定upstream源码。

独立HTTPS/collector `e2_set_diagnostic_v1.py`绑定16个精确合成请求及真实backend profile。
两端分别持久化create-only、fsync的intent/started/terminal，失败或已完成计划均不得续跑。
历史v1/v2/v3实现和已消费ledger不变。

2026-09-05，隔离GB10镜像config ID
`sha256:a727c4ae21174b2c87b3b8ab301fff389fbc052b0a2979b897022e86551e06c6`
完成16个native POST、零重试。八组native/reference完整token数组与渲染字节一致，紧邻repeat
的float32 bits相同。它是本地immutable config/layer身份，不是已发布registry manifest。
扫描保留3199个既有advisory IDs，没有新增ID或依赖变化，不授予生产安全准入。

IMMS首次reference因checkpoint实际共享`model.embed_tokens.weight`而在第一次forward前失败，
原失败记录完整保留。独立审阅的新reference-only纠正计划已完成16forward、0额外native请求；
Go比较保留两份计划身份并通过完整性核验。native与FP32参考概率最大绝对差为
`0.013222754001617432`，是观测而不是已接受质量容差。native的eager执行仍用FlashAttention2/
custom kernels，独立参考则用HF eager attention。

固定诊断结束后其专用容器均已停止，原模型和产品服务没有重启。Python全量189项通过。
正式数值/校准规则与quality authority仍未签发。
完整原始证据见[IMMS报告](../../../IMMS/docs/gb10-set-reranker-2026-09-05.md)。
