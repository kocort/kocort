# 技术方案：将 llama.cpp 从 CGO 静态编译改为动态加载官方预构建库

> **目标**：移除项目中 vendored 的 llama.cpp/ggml 全部 C/C++ 源码及 CGO 绑定，改为运行时从 GitHub Releases 按需下载官方 llama.cpp 预构建共享库（`.dylib`/`.so`/`.dll`），通过 `purego` 纯 Go dlopen 动态加载。

---

## 目录

1. [现状分析](#1-现状分析)
2. [目标架构](#2-目标架构)
3. [官方 Release 共享库分析](#3-官方-release-共享库分析)
4. [函数映射表：现有 CGO → 官方 API](#4-函数映射表现有-cgo--官方-api)
5. [Gap 分析与替代方案](#5-gap-分析与替代方案)
6. [新增模块详细设计](#6-新增模块详细设计)
7. [需要修改的现有文件清单](#7-需要修改的现有文件清单)
8. [需要删除的文件清单](#8-需要删除的文件清单)
9. [分步实施计划](#9-分步实施计划)
10. [风险与测试策略](#10-风险与测试策略)

---

## 1. 现状分析

### 1.1 当前架构

```
Go 业务代码
  └─ internal/localmodel/llamawrapper/  （推理引擎层）
       ├─ engine.go        import "internal/llama"
       ├─ completion.go    import "internal/llama"
       ├─ handler.go       import "internal/llama"
       ├─ kvcache.go       import "internal/llama"
       └─ sequence.go      import "internal/llama"
  └─ internal/localmodel/
       ├─ model_backend_cgo.go      //go:build llamacpp
       ├─ model_backend_nocgo.go    //go:build !llamacpp
       ├─ metadata_llamacpp.go      //go:build llamacpp  → llama.GetModelArch
       └─ metadata_nollamacpp.go    //go:build !llamacpp
  └─ internal/llama/                （CGO 绑定层）
       ├─ llama.go          922 行, #cgo 直接编译 vendored 源码
       ├─ sampling_ext.h    自定义 C 头文件
       ├─ sampling_ext.cpp  137 行, 包装 common_sampler / grammar / schema_to_grammar
       ├─ ggml/src/          ggml 后端 CGO 加载器 (ggml.go 225 行)
       ├─ llama.cpp/         vendored llama.cpp 全部源码（common/, src/, tools/mtmd/）
       └─ ggml/              vendored ggml 全部源码
  └─ CMakeLists.txt          127 行, 编译 ggml GPU 后端动态模块
  └─ CMakePresets.json
  └─ build/                  CMake 构建产物
```

### 1.2 依赖 `internal/llama` 包的文件清单

| 文件 | 使用的 llama 导出 |
|------|-------------------|
| `llamawrapper/engine.go` | `BackendInit`, `GetModelArch`, `ModelParams`, `LoadModelFromFile`, `FreeModel`, `FlashAttentionType`, `NewContextParams`, `NewContextWithModel`, `NewBatch`, `Batch`, `Model`, `Context`, `MtmdContext`, `SamplingContext`, `SamplingParams`, `NewSamplingContext`, `ErrKvCacheFull`, `ErrDecodeAborted` |
| `llamawrapper/completion.go` | `SamplingParams`, `SchemaToGrammar` |
| `llamawrapper/handler.go` | `SamplingParams` (via `toLlamaSampling`) |
| `llamawrapper/kvcache.go` | `Context` |
| `llamawrapper/sequence.go` | `SamplingContext`, `SamplingParams` |
| `localmodel/metadata_llamacpp.go` | `GetModelArch` |
| `localmodel/model_backend_cgo.go` | 间接（通过 `llamawrapper.Engine`） |

### 1.3 `internal/llama/llama.go` 导出的公开 API

**类型**：
- `DeviceID`, `Devices`, `FlashAttentionType`
- `ModelParams`, `Model`
- `ContextParams`, `Context`
- `Batch`
- `SamplingParams`, `SamplingContext`
- `Grammar`, `TokenData`
- `MtmdContext`, `MtmdChunk`

**函数/方法**：
- `BackendInit()`
- `EnumerateGPUs() []Devices`
- `GetModelArch(path) (string, error)`
- `LoadModelFromFile(path, ModelParams) (*Model, error)`
- `FreeModel(*Model)`
- `NewContextParams(...) ContextParams`
- `NewContextWithModel(*Model, ContextParams) (*Context, error)`
- `NewBatch(batchSize, maxSeq, embedSize) (*Batch, error)`
- `NewSamplingContext(*Model, SamplingParams) (*SamplingContext, error)`
- `SchemaToGrammar([]byte) []byte`
- `NewGrammar(grammar, vocabIds, vocabValues, eogTokens) *Grammar`
- `NewMtmdContext(*Context, modelPath) (*MtmdContext, error)`

**Model 方法**：`NumVocab`, `TokenIsEog`, `AddBOSToken`, `ApplyLoraFromFile`, `TokenToPiece`, `Tokenize`, `NEmbd`, `NCtxTrain`, `Vocab`

**Context 方法**：`Decode`, `SetAbortCallback`, `RequestAbort`, `ResetAbort`, `InstallAbortFlagCallback`, `Model`, `KvCacheSeqAdd/Rm/Cp`, `KvCacheClear`, `KvCacheCanShift`, `NCtx`, `Free`, `GetEmbeddingsSeq/Ith`, `GetLogitsIth`, `Synchronize`

**Batch 方法**：`Size`, `NumTokens`, `IsEmbedding`, `Add`, `Clear`, `Free`

**SamplingContext 方法**：`Reset`, `Free`, `Sample`, `Accept`

**Grammar 方法**：`Free`, `Apply`, `Accept`

**MtmdContext 方法**：`Free`, `MultimodalTokenize`

### 1.4 `internal/llama/ggml/src/ggml.go` 功能

- `OnceLoad()` — 从 `lib/kocort/` 目录 dlopen 加载 ggml GPU 后端动态模块（Vulkan/CUDA/ROCm）
- `system{}` — 枚举已加载的 ggml 后端设备信息用于 slog 日志
- `LibPaths()` — 返回搜索的库路径列表
- `discoverOllamaLibPath()` — 自动发现 ollama 安装路径作为 GPU 后端 fallback

---

## 2. 目标架构

```
Go 业务代码（无 CGO 依赖，可交叉编译）
  └─ internal/localmodel/llamawrapper/   ← 推理引擎层（保持接口不变）
       ├─ engine.go        import "internal/llamadl" (替换 "internal/llama")
       ├─ completion.go    import "internal/llamadl"
       ├─ handler.go       import "internal/llamadl"
       ├─ kvcache.go       import "internal/llamadl"
       └─ sequence.go      import "internal/llamadl"
  └─ internal/localmodel/
       ├─ model_backend.go          ← 移除 build tag, 统一使用 llamadl
       ├─ metadata.go               ← 合并, 移除 build tag
       └─ (删除 _cgo/_nocgo 后缀文件)
  └─ internal/llamadl/              ← 全新模块（纯 Go, 零 CGO）
       ├─ library.go        purego dlopen + 函数绑定
       ├─ types.go          C ABI 兼容结构体
       ├─ model.go          Model 封装
       ├─ context.go        Context 封装
       ├─ batch.go          Batch 封装
       ├─ sampler.go        采样链封装（用官方 llama_sampler_chain_* API）
       ├─ grammar.go        语法采样器（用官方 llama_sampler_init_grammar）
       ├─ schema.go         JSON Schema → GBNF 转换（纯 Go 实现）
       ├─ gguf.go           GGUF 元数据读取
       ├─ mtmd.go           多模态封装
       ├─ gpu.go            GPU 设备枚举
       ├─ download.go       按需下载管理
       ├─ platform.go       平台/架构检测
       └─ errors.go         错误定义
  └─ (删除 CMakeLists.txt, CMakePresets.json, build/, internal/llama/)
```

**关键变化**：
1. **`internal/llama/`** → **`internal/llamadl/`**：完全重写，从 CGO 改为 purego 动态加载
2. **`llamawrapper/`**：只需修改 import 路径，所有类型和方法签名保持一致
3. **`model_backend_cgo.go` / `model_backend_nocgo.go`**：合并为一个文件，移除 build tag
4. **`metadata_llamacpp.go` / `metadata_nollamacpp.go`**：合并为一个文件
5. **CMake 构建**：完全删除（GPU 后端也由官方 release 提供）

---

## 3. 官方 Release 共享库分析

### 3.1 Release 地址与命名

```
基础 URL: https://github.com/ggml-org/llama.cpp/releases/download/b{VERSION}/

macOS:
  llama-b{v}-bin-macos-arm64.tar.gz          ← Apple Silicon
  llama-b{v}-bin-macos-arm64-kleidiai.tar.gz ← Apple Silicon + KleidiAI
  llama-b{v}-bin-macos-x64.tar.gz            ← Intel Mac

Linux:
  llama-b{v}-bin-ubuntu-x64.tar.gz           ← CPU only
  llama-b{v}-bin-ubuntu-arm64.tar.gz         ← CPU only
  llama-b{v}-bin-ubuntu-vulkan-x64.tar.gz    ← Vulkan GPU
  llama-b{v}-bin-ubuntu-vulkan-arm64.tar.gz  ← Vulkan GPU
  llama-b{v}-bin-ubuntu-rocm-7.2-x64.tar.gz ← AMD ROCm

Windows:
  llama-b{v}-bin-win-cpu-x64.zip             ← CPU only
  llama-b{v}-bin-win-cpu-arm64.zip           ← CPU only
  llama-b{v}-bin-win-cuda-12.4-x64.zip       ← NVIDIA CUDA 12
  llama-b{v}-bin-win-cuda-13.1-x64.zip       ← NVIDIA CUDA 13
  llama-b{v}-bin-win-vulkan-x64.zip          ← Vulkan
  llama-b{v}-bin-win-hip-radeon-x64.zip      ← AMD HIP
```

### 3.2 Release 包内容（以 macOS arm64 为例）

```
build/bin/
  ├─ llama-cli, llama-server, ...  ← 可执行文件（不需要）
  ├─ libllama.dylib                ← 核心推理库 ★
  ├─ libggml.dylib                 ← ggml 基础库 ★
  ├─ libggml-base.dylib            ← ggml 基础依赖 ★
  ├─ libggml-cpu.dylib             ← CPU 后端 ★
  ├─ libggml-metal.dylib           ← Metal GPU 后端 ★ (macOS)
  ├─ libmtmd.dylib                 ← 多模态库 ★
  └─ libcommon.dylib               ← common 库 (部分 release 有)
build/include/
  └─ llama.h, ggml.h, mtmd.h      ← 头文件（开发参考）
```

> **注意**: `libcommon` 在官方 CMakeLists.txt 中是 `STATIC` 库，**大部分 release 中不包含共享版本**。
> 因此 `common_sampler_*`, `json_schema_to_grammar` 等函数不能通过 dlopen 获取。

### 3.3 我们需要的库文件

| 库 | 用途 | 必须 |
|----|------|------|
| `libllama.{so\|dylib\|dll}` | 模型加载、上下文、decode、tokenize、采样器链、语法 | ✅ 必须 |
| `libggml.{so\|dylib\|dll}` | ggml 基础 + `gguf_*` 函数 + `ggml_backend_dev_*` | ✅ 必须 |
| `libggml-base.{so\|dylib\|dll}` | libggml 的运行时依赖 | ✅ 必须 |
| `libggml-cpu.{so\|dylib\|dll}` | CPU 计算后端 | ✅ 必须 |
| `libggml-metal.dylib` | macOS Metal GPU | ⚡ 按平台 |
| `libggml-vulkan.{so\|dll}` | Vulkan GPU | ⚡ 按变体 |
| `libggml-cuda.{so\|dll}` | CUDA GPU | ⚡ 按变体 |
| `libmtmd.{so\|dylib\|dll}` | 多模态（视觉/音频） | 🔧 可选 |

---

## 4. 函数映射表：现有 CGO → 官方 API

### 4.1 libllama 函数（全部 1:1 映射）

| 当前 CGO 调用 | 官方 libllama 函数 | 签名 |
|---|---|---|
| `C.llama_backend_init()` | `llama_backend_init` | `void()` |
| `C.llama_model_default_params()` | `llama_model_default_params` | `→ llama_model_params` |
| `C.llama_model_load_from_file(path, params)` | `llama_model_load_from_file` | `(char*, params) → model*` |
| `C.llama_model_free(model)` | `llama_model_free` | `(model*) → void` |
| `C.llama_context_default_params()` | `llama_context_default_params` | `→ llama_context_params` |
| `C.llama_init_from_model(model, params)` | `llama_init_from_model` | `(model*, params) → ctx*` |
| `C.llama_free(ctx)` | `llama_free` | `(ctx*) → void` |
| `C.llama_decode(ctx, batch)` | `llama_decode` | `(ctx*, batch) → int32` |
| `C.llama_synchronize(ctx)` | `llama_synchronize` | `(ctx*) → void` |
| `C.llama_tokenize(vocab, text, len, tokens, max, add_sp, parse_sp)` | `llama_tokenize` | `→ int32` |
| `C.llama_token_to_piece(vocab, token, buf, len, lstrip, special)` | `llama_token_to_piece` | `→ int32` |
| `C.llama_get_logits_ith(ctx, i)` | `llama_get_logits_ith` | `→ float*` |
| `C.llama_get_embeddings_seq(ctx, seq_id)` | `llama_get_embeddings_seq` | `→ float*` |
| `C.llama_get_embeddings_ith(ctx, i)` | `llama_get_embeddings_ith` | `→ float*` |
| `C.llama_batch_init(n, embd, seq_max)` | `llama_batch_init` | `→ llama_batch` |
| `C.llama_batch_free(batch)` | `llama_batch_free` | `(batch) → void` |
| `C.llama_get_memory(ctx)` | `llama_get_memory` | `→ memory_t` |
| `C.llama_memory_seq_add(mem, seq, p0, p1, delta)` | `llama_memory_seq_add` | `→ void` |
| `C.llama_memory_seq_rm(mem, seq, p0, p1)` | `llama_memory_seq_rm` | `→ bool` |
| `C.llama_memory_seq_cp(mem, src, dst, p0, p1)` | `llama_memory_seq_cp` | `→ void` |
| `C.llama_memory_clear(mem, data)` | `llama_memory_clear` | `→ void` |
| `C.llama_memory_can_shift(mem)` | `llama_memory_can_shift` | `→ bool` |
| `C.llama_set_abort_callback(ctx, cb, data)` | `llama_set_abort_callback` | `→ void` |
| `C.llama_n_ctx(ctx)` | `llama_n_ctx` | `→ uint32` |
| `C.llama_model_n_embd(model)` | `llama_model_n_embd` | `→ int32` |
| `C.llama_model_n_ctx_train(model)` | `llama_model_n_ctx_train` | `→ int32` |
| `C.llama_model_get_vocab(model)` | `llama_model_get_vocab` | `→ vocab*` |
| `C.llama_get_model(ctx)` | `llama_get_model` | `→ model*` |
| `C.llama_vocab_n_tokens(vocab)` | `llama_vocab_n_tokens` | `→ int32` |
| `C.llama_vocab_is_eog(vocab, token)` | `llama_vocab_is_eog` | `→ bool` |
| `C.llama_vocab_get_add_bos(vocab)` | `llama_vocab_get_add_bos` | `→ bool` |
| `C.llama_adapter_lora_init(model, path)` | `llama_adapter_lora_init` | `→ adapter*` |
| `C.llama_set_adapters_lora(ctx, adapters, n, scales)` | `llama_set_adapters_lora` | `→ int32` |
| `C.llama_log_set(cb, data)` | `llama_log_set` | `→ void` |
| `C.llama_sampler_chain_default_params()` | `llama_sampler_chain_default_params` | `→ params` |
| `C.llama_sampler_chain_init(params)` | `llama_sampler_chain_init` | `→ sampler*` |
| `C.llama_sampler_chain_add(chain, smpl)` | `llama_sampler_chain_add` | `→ void` |
| `C.llama_sampler_init_top_k(k)` | `llama_sampler_init_top_k` | `→ sampler*` |
| `C.llama_sampler_init_top_p(p, min_keep)` | `llama_sampler_init_top_p` | `→ sampler*` |
| `C.llama_sampler_init_min_p(p, min_keep)` | `llama_sampler_init_min_p` | `→ sampler*` |
| `C.llama_sampler_init_typical(p, min_keep)` | `llama_sampler_init_typical` | `→ sampler*` |
| `C.llama_sampler_init_temp(t)` | `llama_sampler_init_temp` | `→ sampler*` |
| `C.llama_sampler_init_dist(seed)` | `llama_sampler_init_dist` | `→ sampler*` |
| `C.llama_sampler_init_penalties(...)` | `llama_sampler_init_penalties` | `→ sampler*` |
| `C.llama_sampler_init_grammar(vocab, grammar, root)` | `llama_sampler_init_grammar` | `→ sampler*` |
| `C.llama_sampler_sample(smpl, ctx, idx)` | `llama_sampler_sample` | `→ token` |
| `C.llama_sampler_accept(smpl, token)` | `llama_sampler_accept` | `→ void` |
| `C.llama_sampler_reset(smpl)` | `llama_sampler_reset` | `→ void` |
| `C.llama_sampler_free(smpl)` | `llama_sampler_free` | `→ void` |
| `C.llama_sampler_apply(smpl, cur_p)` | `llama_sampler_apply` | `→ void` |

### 4.2 libggml / libggml-base 函数

> **重要**: `gguf_*` 和 `ggml_backend_dev_type/get_props/name` 等函数在 **libggml-base** 中导出，
> `ggml_backend_dev_count/get` 和 `ggml_backend_load_all*` 在 **libggml** 中导出。
> dlopen 时两个库都需要加载。

| 当前 CGO 调用 | 官方函数 | 所在库 |
|---|---|---|
| `C.ggml_backend_dev_count()` | `ggml_backend_dev_count` | libggml |
| `C.ggml_backend_dev_get(i)` | `ggml_backend_dev_get` | libggml |
| `C.ggml_backend_dev_type(dev)` | `ggml_backend_dev_type` | **libggml-base** |
| `C.ggml_backend_dev_get_props(dev, props)` | `ggml_backend_dev_get_props` | **libggml-base** |
| `C.ggml_backend_load_all_from_path(path)` | `ggml_backend_load_all_from_path` | libggml |
| `C.gguf_init_from_file(path, params)` | `gguf_init_from_file` | **libggml-base** |
| `C.gguf_find_key(ctx, key)` | `gguf_find_key` | **libggml-base** |
| `C.gguf_get_val_str(ctx, key_id)` | `gguf_get_val_str` | **libggml-base** |
| `C.gguf_free(ctx)` | `gguf_free` | **libggml-base** |

### 4.3 libmtmd 函数

| 当前 CGO 调用 | 官方 libmtmd 函数 |
|---|---|
| `C.mtmd_context_params_default()` | `mtmd_context_params_default` |
| `C.mtmd_init_from_file(path, model, params)` | `mtmd_init_from_file` |
| `C.mtmd_free(ctx)` | `mtmd_free` |
| `C.mtmd_input_chunks_init()` | `mtmd_input_chunks_init` |
| `C.mtmd_input_chunks_free(chunks)` | `mtmd_input_chunks_free` |
| `C.mtmd_input_chunks_size(chunks)` | `mtmd_input_chunks_size` |
| `C.mtmd_input_chunks_get(chunks, i)` | `mtmd_input_chunks_get` |
| `C.mtmd_input_chunk_get_type(chunk)` | `mtmd_input_chunk_get_type` |
| `C.mtmd_input_chunk_get_tokens_text(chunk, n)` | `mtmd_input_chunk_get_tokens_text` |
| `C.mtmd_input_chunk_get_n_tokens(chunk)` | `mtmd_input_chunk_get_n_tokens` |
| `C.mtmd_tokenize(ctx, chunks, text, bitmaps, n)` | `mtmd_tokenize` |
| `C.mtmd_encode_chunk(ctx, chunk)` | `mtmd_encode_chunk` |
| `C.mtmd_get_output_embd(ctx)` | `mtmd_get_output_embd` |
| `C.mtmd_default_marker()` | `mtmd_default_marker` |
| `C.mtmd_bitmap_init(nx, ny, data)` | `mtmd_bitmap_init` |
| `C.mtmd_bitmap_free(bitmap)` | `mtmd_bitmap_free` |
| `C.mtmd_helper_bitmap_init_from_buf(ctx, data, len)` | `mtmd_helper_bitmap_init_from_buf` |
| `C.mtmd_input_text_init(marker, add_sp, parse_sp)` | 见 mtmd-helper.h |
| `C.mtmd_input_text_free(text)` | 见 mtmd-helper.h |

---

## 5. Gap 分析与替代方案

### 5.1 不在官方共享库中的函数（来自 `sampling_ext.cpp`）

| 当前自定义函数 | 用途 | 替代方案 |
|---|---|---|
| `common_sampler_cinit(model, params)` | 创建采样器 | **用 `llama_sampler_chain_init` + `llama_sampler_chain_add` 手动构建采样链** |
| `common_sampler_csample(s, ctx, idx)` | 采样一个 token | **用 `llama_sampler_sample(smpl, ctx, idx)`** |
| `common_sampler_caccept(s, id, grammar)` | 接受 token | **用 `llama_sampler_accept(smpl, token)`** |
| `common_sampler_creset(s)` | 重置采样器 | **用 `llama_sampler_reset(smpl)`** |
| `common_sampler_cfree(s)` | 释放采样器 | **用 `llama_sampler_free(smpl)`** |
| `schema_to_grammar(json)` | JSON Schema → GBNF | **纯 Go 重新实现** |
| `grammar_init(g, tokens, pieces, eog)` | 自定义 grammar | **用 `llama_sampler_init_grammar(vocab, str, "root")`** |
| `grammar_apply(g, token_data)` | 应用 grammar 到 logits | **用 `llama_sampler_apply(smpl, &cur_p)`** |
| `grammar_accept(g, id)` | grammar 接受 token | **用 `llama_sampler_accept(smpl, token)`** |
| `grammar_free(g)` | 释放 grammar | **用 `llama_sampler_free(smpl)`** |

### 5.2 `ggml.go` 中的功能替代

| 当前 ggml.go 功能 | 替代方案 |
|---|---|
| `OnceLoad()` — dlopen GPU 后端 | **`llamadl/library.go` 中调用 `ggml_backend_load_all_from_path`** |
| `system{}` 日志输出 | **在 `llamadl/gpu.go` 中用 purego 调用 `ggml_backend_dev_*` 枚举** |
| `LibPaths()` | **迁移到 `llamadl/platform.go`** |
| `discoverOllamaLibPath()` | **迁移到 `llamadl/platform.go`** |

### 5.3 `mtmd_helper_*` 函数

经实际验证（b8721 release），`mtmd_helper_*` 函数 **已从 libmtmd 中导出**，可直接通过 dlopen 调用：
- ✅ `mtmd_helper_bitmap_init_from_buf` — 直接调用，无需 Go 侧图片解码
- ✅ `mtmd_helper_bitmap_init_from_file` — 直接调用
- ✅ `mtmd_helper_decode_image_chunk` — 直接调用
- ✅ `mtmd_helper_eval_chunk_single` / `mtmd_helper_eval_chunks` — 直接调用
- ✅ `mtmd_helper_get_n_tokens` / `mtmd_helper_get_n_pos` — 直接调用

> **注意**: `mtmd_input_text` 结构体仍需在 Go 中手动构造（3 个字段：`text *byte`, `add_special bool`, `parse_special bool`）

---

## 6. 新增模块详细设计

### 6.1 `internal/llamadl/library.go` — 动态库加载与函数绑定

```go
package llamadl

import "github.com/ebitengine/purego"

// Library 持有所有 dlopen 的句柄和函数指针
type Library struct {
    libGgmlBase uintptr // 必须最先加载（被 libggml, libllama 依赖）
    libGgml     uintptr
    libLlama    uintptr
    libMtmd     uintptr // 可选

    // ── libllama 函数 ──
    llamaBackendInit           func()
    llamaModelDefaultParams    func() ModelParams   // 返回值是结构体，需要特殊处理
    llamaModelLoadFromFile     func(path *byte, params uintptr) uintptr
    llamaModelFree             func(model uintptr)
    // ... (约60个函数，见第4节完整映射表)

    // ── libggml 函数 ──
    ggmlBackendDevCount        func() uintptr
    ggmlBackendDevGet          func(i uintptr) uintptr
    ggmlBackendLoadAllFromPath func(path *byte)

    // ── libggml-base 函数（注意：gguf_* 和 backend_dev_type/get_props 在 libggml-base 中）──
    ggmlBackendDevType         func(dev uintptr) int32
    ggmlBackendDevGetProps     func(dev uintptr, props uintptr)
    ggufInitFromFile           func(path *byte, params uintptr) uintptr
    ggufFindKey                func(ctx uintptr, key *byte) int32
    ggufGetValStr              func(ctx uintptr, keyId int32) *byte
    ggufFree                   func(ctx uintptr)

    // ── libmtmd 函数（可选） ──
    mtmdInitFromFile           func(path *byte, model uintptr, params uintptr) uintptr
    mtmdFree                   func(ctx uintptr)
    // ...
}

// Open 打开所有需要的共享库并注册函数
func Open(libDir string) (*Library, error) {
    // 加载顺序重要（有依赖关系）：
    // 1. dlopen libggml-base （最底层，被所有其他库依赖）
    // 2. dlopen libggml-cpu  （CPU 计算后端）
    // 3. dlopen libggml      （backend 注册/加载器，依赖 libggml-base）
    // 4. dlopen libllama     （核心推理，依赖 libggml + libggml-base）
    // 5. purego.RegisterLibFunc 注册所有函数
    // 6. 调用 ggml_backend_load_all_from_path(libDir) 加载 GPU 后端
    //    （这会自动发现并加载同目录下的 libggml-metal/vulkan/cuda 等）
    // 7. 可选: dlopen libmtmd （多模态，依赖 libllama）
}

// Close 关闭所有库句柄
func (l *Library) Close() {
    // purego.Dlclose
}
```

**purego 函数注册模式**：

```go
// 对于简单函数：
purego.RegisterLibFunc(&l.llamaBackendInit, l.libLlama, "llama_backend_init")

// 对于返回结构体的函数（purego 可能不支持直接返回大结构体）：
// 方案A: 如果 purego 支持 —— 直接注册
// 方案B: 用 purego.SyscallN 手动调用，通过指针传回
```

**关于返回大结构体的函数**（如 `llama_model_default_params()` 返回 `llama_model_params`）：

purego 对 C ABI 的结构体返回值支持取决于结构体大小。如果结构体 > 16 字节（在大多数 ABI 下），C 编译器实际上会通过隐式指针参数返回。purego 处理此问题的方式：

- **方案 A**：封装一个 Go 函数，使用 `purego.SyscallN` 手动调用，将结果写入预分配的内存
- **方案 B**：使用 `_default_params` 系列函数后，再通过指针修改字段值

**推荐方案 A**：对所有 `_default_params()` 函数使用 `purego.SyscallN`。

### 6.2 `internal/llamadl/types.go` — C ABI 兼容结构体

```go
package llamadl

import "unsafe"

// 所有结构体必须与 C 端的内存布局完全一致。
// 使用 unsafe.Sizeof / unsafe.Alignof 在测试中验证。

// llama_model_params (简化版，只保留用到的字段)
type cModelParams struct {
    Devices                  uintptr // ggml_backend_dev_t *
    TensorBuftOverrides      uintptr // const struct llama_model_tensor_buft_override *
    NGPULayers               int32
    SplitMode                int32   // enum llama_split_mode
    MainGPU                  int32
    TensorSplit              uintptr // const float *
    ProgressCallback         uintptr // llama_progress_callback
    ProgressCallbackUserData uintptr // void *
    KVOverrides              uintptr // const struct llama_model_kv_override *
    VocabOnly                bool
    UseMmap                  bool
    UseDirectIO              bool
    UseMlock                 bool
    CheckTensors             bool
    UseExtraBuffs            bool
    NoHost                   bool
    NoAlloc                  bool
}

// llama_context_params
type cContextParams struct {
    NCtx            uint32
    NBatch          uint32
    NUbatch         uint32
    NSeqMax         uint32
    NThreads        int32
    NThreadsBatch   int32
    RopeScalingType int32
    PoolingType     int32
    AttentionType   int32
    FlashAttnType   int32
    RopeFreqBase    float32
    RopeFreqScale   float32
    YarnExtFactor   float32
    YarnAttnFactor  float32
    YarnBetaFast    float32
    YarnBetaSlow    float32
    YarnOrigCtx     uint32
    DefragThold     float32
    CbEval          uintptr
    CbEvalUserData  uintptr
    TypeK           int32 // enum ggml_type
    TypeV           int32 // enum ggml_type
    AbortCallback   uintptr
    AbortCallbackData uintptr
    Embeddings      bool
    OffloadKQV      bool
    NoPerf          bool
    OpOffload       bool
    SWAFull         bool
    KVUnified       bool
    Samplers        uintptr
    NSamplers       uintptr
}

// llama_batch
type cBatch struct {
    NTokens int32
    _pad    [4]byte  // 对齐，根据平台调整
    Token   uintptr  // llama_token *
    Embd    uintptr  // float *
    Pos     uintptr  // llama_pos *
    NSeqID  uintptr  // int32_t *
    SeqID   uintptr  // llama_seq_id **
    Logits  uintptr  // int8_t *
}

// llama_token_data
type cTokenData struct {
    ID    int32
    Logit float32
    P     float32
}

// llama_token_data_array
type cTokenDataArray struct {
    Data     uintptr // llama_token_data *
    Size     uintptr // size_t
    Selected int64
    Sorted   bool
    _pad     [7]byte
}

// llama_sampler_chain_params
type cSamplerChainParams struct {
    NoPerf bool
    _pad   [7]byte
}

// gguf_init_params
type cGgufInitParams struct {
    NoAlloc bool
    _pad    [7]byte
    Ctx     uintptr // struct ggml_context **
}

// ggml_backend_dev_props (用于 GPU 枚举)
type cBackendDevProps struct {
    Name    [128]byte
    Desc    [256]byte
    ID      [128]byte
    Library [128]byte
    // ... 其他字段根据实际 ggml.h 定义
}

// 辅助函数
func cstr(s string) *byte {
    b := append([]byte(s), 0)
    return &b[0]
}

func gostr(p *byte) string {
    if p == nil { return "" }
    var buf []byte
    for ptr := unsafe.Pointer(p); *(*byte)(ptr) != 0; ptr = unsafe.Add(ptr, 1) {
        buf = append(buf, *(*byte)(ptr))
    }
    return string(buf)
}
```

> ⚠️ **关键**：每个结构体的大小和字段偏移必须通过测试验证。建议写一个 C 程序输出各结构体的 `sizeof` 和 `offsetof`，然后在 Go 测试中比对。

### 6.3 `internal/llamadl/model.go` — Model 封装

```go
package llamadl

// Model 包装 llama_model 指针
type Model struct {
    ptr   uintptr       // *C.llama_model
    lib   *Library
    vocab uintptr       // cached *C.llama_vocab
}

// 公开 API（与现有 llama.Model 完全一致）
func (m *Model) NumVocab() int
func (m *Model) TokenIsEog(token int) bool
func (m *Model) AddBOSToken() bool
func (m *Model) TokenToPiece(token int) string
func (m *Model) Tokenize(text string, addSpecial bool, parseSpecial bool) ([]int, error)
func (m *Model) NEmbd() int
func (m *Model) NCtxTrain() int
func (m *Model) ApplyLoraFromFile(context *Context, loraPath string, scale float32, threads int) error
func (m *Model) Vocab() uintptr  // 内部使用
```

### 6.4 `internal/llamadl/context.go` — Context 封装

```go
package llamadl

// Context 包装 llama_context 指针
type Context struct {
    ptr        uintptr   // *C.llama_context
    lib        *Library
    numThreads int
    // abort 相关
    abortFlag  atomic.Bool
}

// 公开 API（与现有完全一致）
func (c *Context) Decode(batch *Batch) error
func (c *Context) SetAbortCallback(callback func() bool)
func (c *Context) RequestAbort()
func (c *Context) ResetAbort()
func (c *Context) InstallAbortFlagCallback()
func (c *Context) Model() *Model
func (c *Context) KvCacheSeqAdd(seqId, p0, p1, delta int)
func (c *Context) KvCacheSeqRm(seqId, p0, p1 int) bool
func (c *Context) KvCacheSeqCp(srcSeqId, dstSeqId, p0, p1 int)
func (c *Context) KvCacheClear()
func (c *Context) KvCacheCanShift() bool
func (c *Context) NCtx() int
func (c *Context) Free()
func (c *Context) GetEmbeddingsSeq(seqId int) []float32
func (c *Context) GetEmbeddingsIth(i int) []float32
func (c *Context) GetLogitsIth(i int) []float32
func (c *Context) Synchronize()
```

### 6.5 `internal/llamadl/batch.go` — Batch 封装

```go
package llamadl

// Batch 包装 llama_batch + Go 侧管理
type Batch struct {
    c         cBatch    // C 结构体
    lib       *Library
    batchSize int
    maxSeq    int
    embedSize int

    // Go 侧分配的内存（用于持有 token/pos/seq_id 等数组）
    tokenBuf  []int32
    posBuf    []int32
    seqIdBuf  [][]int32
    logitsBuf []int8
    embdBuf   []float32
}

// 公开 API（与现有完全一致）
func NewBatch(lib *Library, batchSize, maxSeq, embedSize int) (*Batch, error)
func (b *Batch) Size() int
func (b *Batch) NumTokens() int
func (b *Batch) IsEmbedding() bool
func (b *Batch) Add(token int, embed []float32, pos int, logits bool, seqIds ...int)
func (b *Batch) Clear()
func (b *Batch) Free()
```

> **关键变化**：原来 `llama_batch_init` 在 C 端分配内存。新版需要在 Go 端分配数组，然后把指针设置到 `cBatch` 结构体中。这样可以避免跨 FFI 内存管理的复杂性。也可以继续调用 `llama_batch_init` 通过 FFI，然后直接操作返回的结构体中的指针。

### 6.6 `internal/llamadl/sampler.go` — 采样器链

```go
package llamadl

// SamplingParams 与现有 llama.SamplingParams 完全一致
type SamplingParams struct {
    TopK           int
    TopP           float32
    MinP           float32
    TypicalP       float32
    Temp           float32
    RepeatLastN    int
    PenaltyRepeat  float32
    PenaltyFreq    float32
    PenaltyPresent float32
    Seed           uint32
    Grammar        string
}

// SamplingContext 包装 llama_sampler 链
type SamplingContext struct {
    chain uintptr   // *llama_sampler (chain)
    lib   *Library
}

// NewSamplingContext 用官方 sampler chain API 创建采样器
// 等价于当前的 common_sampler_cinit
func NewSamplingContext(lib *Library, model *Model, params SamplingParams) (*SamplingContext, error) {
    chainParams := lib.llamaSamplerChainDefaultParams()
    chain := lib.llamaSamplerChainInit(chainParams)

    // 1. penalties (必须在 top-k 之前)
    if params.RepeatLastN != 0 {
        lib.llamaSamplerChainAdd(chain,
            lib.llamaSamplerInitPenalties(
                int32(params.RepeatLastN),
                params.PenaltyRepeat,
                params.PenaltyFreq,
                params.PenaltyPresent))
    }

    // 2. top-k
    if params.TopK > 0 {
        lib.llamaSamplerChainAdd(chain, lib.llamaSamplerInitTopK(int32(params.TopK)))
    }

    // 3. typical-p
    if params.TypicalP > 0 && params.TypicalP < 1.0 {
        lib.llamaSamplerChainAdd(chain, lib.llamaSamplerInitTypical(params.TypicalP, 1))
    }

    // 4. top-p
    if params.TopP > 0 && params.TopP < 1.0 {
        lib.llamaSamplerChainAdd(chain, lib.llamaSamplerInitTopP(params.TopP, 1))
    }

    // 5. min-p
    if params.MinP > 0 {
        lib.llamaSamplerChainAdd(chain, lib.llamaSamplerInitMinP(params.MinP, 1))
    }

    // 6. temperature
    if params.Temp > 0 {
        lib.llamaSamplerChainAdd(chain, lib.llamaSamplerInitTemp(params.Temp))
    } else {
        // temp=0 → greedy
        lib.llamaSamplerChainAdd(chain, lib.llamaSamplerInitGreedy())
    }

    // 7. final dist sampler (token 选择)
    if params.Temp > 0 {
        lib.llamaSamplerChainAdd(chain, lib.llamaSamplerInitDist(params.Seed))
    }

    // 8. grammar (如果有)
    if params.Grammar != "" {
        vocab := lib.llamaModelGetVocab(model.ptr)
        lib.llamaSamplerChainAdd(chain,
            lib.llamaSamplerInitGrammar(vocab, params.Grammar, "root"))
    }

    return &SamplingContext{chain: chain, lib: lib}, nil
}

func (s *SamplingContext) Sample(ctx *Context, idx int) int {
    return int(s.lib.llamaSamplerSample(s.chain, ctx.ptr, int32(idx)))
}

func (s *SamplingContext) Accept(id int, applyGrammar bool) {
    // 官方 API 的 accept 总是接受。grammar 作为 chain 的一部分自动处理。
    s.lib.llamaSamplerAccept(s.chain, int32(id))
}

func (s *SamplingContext) Reset() {
    s.lib.llamaSamplerReset(s.chain)
}

func (s *SamplingContext) Free() {
    if s.chain != 0 {
        s.lib.llamaSamplerFree(s.chain)
        s.chain = 0
    }
}
```

### 6.7 `internal/llamadl/grammar.go` — Grammar 封装

```go
package llamadl

import "sync"

// Grammar 使用官方 llama_sampler_init_grammar 替代自定义 grammar
type Grammar struct {
    sampler uintptr   // *llama_sampler
    lib     *Library
    mu      sync.Mutex
}

type TokenData struct {
    ID    int32
    Logit float32
}

// NewGrammar 创建 grammar 采样器
// 不再需要 vocabIds/vocabValues/eogTokens，官方 API 直接接受 vocab 指针
func NewGrammar(lib *Library, vocab uintptr, grammar string) *Grammar {
    smpl := lib.llamaSamplerInitGrammar(vocab, grammar, "root")
    if smpl == 0 {
        return nil
    }
    return &Grammar{sampler: smpl, lib: lib}
}

func (g *Grammar) Apply(tokens []TokenData) {
    g.mu.Lock()
    defer g.mu.Unlock()
    if g.sampler == 0 { return }

    // 构建 llama_token_data_array
    tds := make([]cTokenData, len(tokens))
    for i, t := range tokens {
        tds[i] = cTokenData{ID: t.ID, Logit: t.Logit, P: 0}
    }
    arr := cTokenDataArray{
        Data:     uintptr(unsafe.Pointer(&tds[0])),
        Size:     uintptr(len(tds)),
        Selected: -1,
        Sorted:   false,
    }
    g.lib.llamaSamplerApply(g.sampler, uintptr(unsafe.Pointer(&arr)))

    // 回写 logit
    for i := range tokens {
        tokens[i].Logit = tds[i].Logit
    }
}

func (g *Grammar) Accept(token int32) {
    g.mu.Lock()
    defer g.mu.Unlock()
    if g.sampler == 0 { return }
    g.lib.llamaSamplerAccept(g.sampler, token)
}

func (g *Grammar) Free() {
    g.mu.Lock()
    defer g.mu.Unlock()
    if g.sampler != 0 {
        g.lib.llamaSamplerFree(g.sampler)
        g.sampler = 0
    }
}
```

> **签名变化**：`NewGrammar` 不再需要 `vocabIds`, `vocabValues`, `eogTokens` 参数（这些是自定义 grammar 实现需要的），改为传入 `vocab uintptr`。`llamawrapper/engine.go` 中 `Grammar` 的使用点需要相应调整。

### 6.8 `internal/llamadl/schema.go` — JSON Schema → GBNF（纯 Go）

```go
package llamadl

import "encoding/json"

// SchemaToGrammar 将 JSON Schema 转换为 GBNF 语法字符串
// 返回 nil 表示输入无效
func SchemaToGrammar(schema []byte) []byte {
    var parsed map[string]any
    if err := json.Unmarshal(schema, &parsed); err != nil {
        return nil
    }
    gbnf := schemaToGBNF(parsed)
    if gbnf == "" {
        return nil
    }
    return []byte(gbnf)
}

// schemaToGBNF 实现 JSON Schema → GBNF 规则生成
// 需要支持的 Schema 类型：
// - type: "string", "number", "integer", "boolean", "null", "object", "array"
// - properties + required
// - items (数组元素)
// - enum
// - oneOf / anyOf
// - $ref (简单引用解析)
// - additionalProperties
//
// 参考实现: llama.cpp/common/json-schema-to-grammar.cpp (~800 行 C++)
// 需要翻译为约 300-400 行 Go 代码
func schemaToGBNF(schema map[string]any) string {
    // 实现逻辑...
}
```

> 建议的实现策略：从 llama.cpp 的 `json-schema-to-grammar.cpp` 翻译。核心逻辑是递归遍历 JSON Schema 节点，为每个节点生成对应的 GBNF 规则。

### 6.9 `internal/llamadl/gguf.go` — GGUF 元数据读取

```go
package llamadl

// GetModelArch 从 GGUF 文件读取 general.architecture 元数据
func GetModelArch(lib *Library, modelPath string) (string, error) {
    path := cstr(modelPath)
    params := cGgufInitParams{NoAlloc: true, Ctx: 0}
    ctx := lib.ggufInitFromFile(path, uintptr(unsafe.Pointer(&params)))
    if ctx == 0 {
        return "", errors.New("unable to load model file")
    }
    defer lib.ggufFree(ctx)

    key := cstr("general.architecture")
    idx := lib.ggufFindKey(ctx, key)
    if idx < 0 {
        return "", errors.New("unknown model architecture")
    }

    archPtr := lib.ggufGetValStr(ctx, idx)
    return gostr(archPtr), nil
}
```

### 6.10 `internal/llamadl/gpu.go` — GPU 枚举

```go
package llamadl

type DeviceID struct {
    ID      string `json:"id"`
    Library string `json:"backend,omitempty"`
}

type Devices struct {
    DeviceID
    LlamaID uint64
}

func EnumerateGPUs(lib *Library) []Devices {
    var ids []Devices
    count := lib.ggmlBackendDevCount()
    for i := uintptr(0); i < count; i++ {
        device := lib.ggmlBackendDevGet(i)
        devType := lib.ggmlBackendDevType(device)
        // GGML_BACKEND_DEVICE_TYPE_GPU = 1, IGPU = 2
        if devType == 1 || devType == 2 {
            var props cBackendDevProps
            lib.ggmlBackendDevGetProps(device, uintptr(unsafe.Pointer(&props)))
            ids = append(ids, Devices{
                DeviceID: DeviceID{
                    ID:      gostr(&props.ID[0]),
                    Library: gostr(&props.Library[0]),
                },
                LlamaID: uint64(i),
            })
        }
    }
    return ids
}
```

### 6.11 `internal/llamadl/download.go` — 按需下载管理

```go
package llamadl

import (
    "fmt"
    "os"
    "path/filepath"
    "runtime"
)

const (
    // 锁定兼容的 llama.cpp 版本
    LlamaCppVersion    = "b8720"
    GithubReleaseBase  = "https://github.com/ggml-org/llama.cpp/releases/download"
)

// DownloadConfig 下载配置
type DownloadConfig struct {
    Version   string // 默认 LlamaCppVersion
    CacheDir  string // 默认 ~/.kocort/lib/
    GPUType   string // "cpu", "vulkan", "cuda-12.4", "cuda-13.1", "rocm-7.2", "hip"
    ProxyURL  string // HTTP 代理
}

// EnsureLibraries 确保本地已有对应版本的共享库，没有则下载
// 返回库文件所在目录路径
func EnsureLibraries(cfg DownloadConfig) (string, error) {
    if cfg.Version == "" {
        cfg.Version = LlamaCppVersion
    }
    if cfg.CacheDir == "" {
        home, _ := os.UserHomeDir()
        cfg.CacheDir = filepath.Join(home, ".kocort", "lib")
    }

    targetDir := filepath.Join(cfg.CacheDir, "llama-"+cfg.Version)

    // 检查是否已下载
    if librariesExist(targetDir) {
        return targetDir, nil
    }

    // 确定下载 URL
    url := buildDownloadURL(cfg.Version, cfg.GPUType)

    // 下载并解压
    if err := downloadAndExtract(url, targetDir, cfg.ProxyURL); err != nil {
        return "", fmt.Errorf("download llama.cpp libraries: %w", err)
    }

    return targetDir, nil
}

// buildDownloadURL 根据 OS/Arch/GPU 构建下载 URL
func buildDownloadURL(version, gpuType string) string {
    var suffix string
    switch runtime.GOOS {
    case "darwin":
        if runtime.GOARCH == "arm64" {
            suffix = "macos-arm64"
        } else {
            suffix = "macos-x64"
        }
    case "linux":
        arch := "x64"
        if runtime.GOARCH == "arm64" { arch = "arm64" }
        switch gpuType {
        case "vulkan":
            suffix = fmt.Sprintf("ubuntu-vulkan-%s", arch)
        case "rocm-7.2":
            suffix = fmt.Sprintf("ubuntu-rocm-7.2-%s", arch)
        default:
            suffix = fmt.Sprintf("ubuntu-%s", arch)
        }
    case "windows":
        arch := "x64"
        if runtime.GOARCH == "arm64" { arch = "arm64" }
        switch gpuType {
        case "cuda-12.4":
            suffix = fmt.Sprintf("win-cuda-12.4-%s", arch)
        case "cuda-13.1":
            suffix = fmt.Sprintf("win-cuda-13.1-%s", arch)
        case "vulkan":
            suffix = fmt.Sprintf("win-vulkan-%s", arch)
        case "hip":
            suffix = fmt.Sprintf("win-hip-radeon-%s", arch)
        default:
            suffix = fmt.Sprintf("win-cpu-%s", arch)
        }
    }

    ext := "tar.gz"
    if runtime.GOOS == "windows" { ext = "zip" }

    return fmt.Sprintf("%s/%s/llama-%s-bin-%s.%s",
        GithubReleaseBase, version, version, suffix, ext)
}

// librariesExist 检查目标目录是否包含必需的库文件
func librariesExist(dir string) bool {
    required := requiredLibNames()
    for _, name := range required {
        if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
            return false
        }
    }
    return true
}

// requiredLibNames 返回当前平台必需的库文件名
func requiredLibNames() []string {
    switch runtime.GOOS {
    case "darwin":
        return []string{"libllama.dylib", "libggml.dylib", "libggml-base.dylib", "libggml-cpu.dylib"}
    case "linux":
        return []string{"libllama.so", "libggml.so", "libggml-base.so", "libggml-cpu.so"}
    case "windows":
        return []string{"llama.dll", "ggml.dll", "ggml-base.dll", "ggml-cpu.dll"}
    }
    return nil
}

// downloadAndExtract 下载并解压到目标目录
func downloadAndExtract(url, targetDir, proxy string) error {
    // 复用现有 localmodel.go 的 DynamicHTTPClient 支持代理
    // 1. HTTP GET 下载
    // 2. tar.gz / zip 解压
    // 3. 从解压目录中找到 lib 文件并移动到 targetDir
}
```

### 6.12 `internal/llamadl/platform.go` — 平台工具

```go
package llamadl

import (
    "os"
    "path/filepath"
    "runtime"
    "strings"
)

// LibExtension 返回当前平台的共享库扩展名
func LibExtension() string {
    switch runtime.GOOS {
    case "darwin": return ".dylib"
    case "windows": return ".dll"
    default: return ".so"
    }
}

// LibPrefix 返回当前平台的共享库前缀
func LibPrefix() string {
    if runtime.GOOS == "windows" { return "" }
    return "lib"
}

// LibName 构建库文件全名
func LibName(base string) string {
    return LibPrefix() + base + LibExtension()
}

// discoverOllamaLibPath 与现有 ggml.go 中的逻辑一致
func discoverOllamaLibPath() string { ... }

// defaultLibSearchPaths 返回默认的库搜索路径
func defaultLibSearchPaths() []string { ... }
```

### 6.13 `internal/llamadl/mtmd.go` — 多模态

```go
package llamadl

// MtmdContext 包装 mtmd_context
type MtmdContext struct {
    ptr uintptr
    lib *Library
}

type MtmdChunk struct {
    Embed  []float32
    Tokens []int
}

func NewMtmdContext(lib *Library, ctx *Context, modelPath string) (*MtmdContext, error) {
    // mtmd_context_params_default → mtmd_init_from_file
}

func (c *MtmdContext) Free() { ... }

func (c *MtmdContext) MultimodalTokenize(ctx *Context, data []byte) ([]MtmdChunk, error) {
    // 1. 调用 mtmd_helper_bitmap_init_from_buf(ctx, data, len) ← 已确认在 libmtmd 中导出
    //    （内部自动处理 JPEG/PNG 解码，无需 Go 侧图片处理）
    // 2. 构造 mtmd_input_text 结构体（纯内存操作）
    // 3. mtmd_tokenize → mtmd_encode_chunk → mtmd_get_output_embd
    // 4. 返回 chunks
}
```

### 6.14 `internal/llamadl/errors.go`

```go
package llamadl

import "errors"

var (
    ErrKvCacheFull   = errors.New("could not find a kv cache slot")
    ErrDecodeAborted = errors.New("llama decode aborted")
    ErrLibNotLoaded  = errors.New("llama.cpp library not loaded")
)
```

---

## 7. 需要修改的现有文件清单

### 7.1 `llamawrapper/engine.go`

**修改内容**：
```diff
- import "github.com/kocort/kocort/internal/llama"
+ import "github.com/kocort/kocort/internal/llamadl"
```

所有 `llama.XXX` → `llamadl.XXX`。需要修改的具体引用：

| 行号 | 现有 | 替换为 |
|------|------|--------|
| 19 | `"github.com/kocort/kocort/internal/llama"` | `"github.com/kocort/kocort/internal/llamadl"` |
| 31 | `model *llama.Model` | `model *llamadl.Model` |
| 32 | `ctx   *llama.Context` | `ctx   *llamadl.Context` |
| 33 | `image *llama.MtmdContext` | `image *llamadl.MtmdContext` |
| 58 | `llama.BackendInit()` | `llamadl.BackendInit()` |
| 125 | `llama.GetModelArch(...)` | `llamadl.GetModelArch(...)` |
| 130 | `llama.ModelParams{...}` | `llamadl.ModelParams{...}` |
| 139 | `llama.LoadModelFromFile(...)` | `llamadl.LoadModelFromFile(...)` |
| 147 | `llama.FlashAttentionType(...)` | `llamadl.FlashAttentionType(...)` |
| 148 | `llama.NewContextParams(...)` | `llamadl.NewContextParams(...)` |
| 149 | `llama.NewContextWithModel(...)` | `llamadl.NewContextWithModel(...)` |
| 151,162,275 | `llama.FreeModel(...)` | `llamadl.FreeModel(...)` |
| 203 | `llama.NewBatch(...)` | `llamadl.NewBatch(...)` |
| 211 | `*llama.Batch` | `*llamadl.Batch` |
| 221 | `&llama.Batch{}` | `&llamadl.Batch{}` |
| 230 | `llama.ErrDecodeAborted` | `llamadl.ErrDecodeAborted` |
| 327 | `*llama.SamplingContext` | `*llamadl.SamplingContext` |
| 329 | `llama.NewSamplingContext(...)` | `llamadl.NewSamplingContext(...)` |
| 449 | `llama.Batch` | `llamadl.Batch` |
| 643 | `*llama.Model` | `*llamadl.Model` |

**额外改动**: `NewEngine` 需要接收 `*Library` 参数或者通过全局初始化传入。

推荐方案：在 `EngineConfig` 中增加 `Library *llamadl.Library` 字段，或者使用包级全局变量。

### 7.2 `llamawrapper/completion.go`

| 行号 | 修改 |
|------|------|
| 11 | import 替换 |
| 86 | `llama.SchemaToGrammar(...)` → `llamadl.SchemaToGrammar(...)` |
| 105 | `&llama.SamplingParams{...}` → `&llamadl.SamplingParams{...}` |

### 7.3 `llamawrapper/handler.go`

| 行号 | 修改 |
|------|------|
| 14 | import 替换 |
| 463 | `*llama.SamplingParams` → `*llamadl.SamplingParams` |
| 467 | `&llama.SamplingParams{...}` → `&llamadl.SamplingParams{...}` |

### 7.4 `llamawrapper/kvcache.go`

| 行号 | 修改 |
|------|------|
| 7 | import 替换 |
| 25 | `*llama.Context` → `*llamadl.Context` |
| 31 | `*llama.Context` → `*llamadl.Context` |

### 7.5 `llamawrapper/sequence.go`

| 行号 | 修改 |
|------|------|
| 8 | import 替换 |
| 44 | `*llama.SamplingContext` → `*llamadl.SamplingContext` |
| 82 | `*llama.SamplingParams` → `*llamadl.SamplingParams` |

### 7.6 `localmodel/model_backend_cgo.go` → 重命名为 `model_backend_engine.go`

- 移除 `//go:build llamacpp` 行
- 无需修改 import（它只 import `llamawrapper`，不直接 import `llama`）

### 7.7 `localmodel/model_backend_nocgo.go` → 删除

功能不再需要（所有构建都支持动态加载）。

如果需要保留优雅降级（库未下载时返回 stub），可以在 `model_backend_engine.go` 的 `Start` 中检测 library 是否加载成功。

### 7.8 `localmodel/metadata_llamacpp.go` → 重命名为 `metadata.go`

```diff
- //go:build llamacpp
  package localmodel

- import "github.com/kocort/kocort/internal/llama"
+ import "github.com/kocort/kocort/internal/llamadl"

  func detectModelThinkingDefault(modelPath string) (bool, bool) {
-     arch, err := llama.GetModelArch(modelPath)
+     arch, err := llamadl.GetModelArch(globalLib, modelPath)
      ...
  }
```

### 7.9 `localmodel/metadata_nollamacpp.go` → 删除

### 7.10 `go.mod`

```diff
+ require github.com/ebitengine/purego v0.8.x
```

### 7.11 `llamawrapper/doc.go`

更新注释描述。

---

## 8. 需要删除的文件清单

### 8.1 C/C++ 源码（核心删除）

```
internal/llama/llama.go               ← 922 行 CGO 绑定
internal/llama/llama_test.go
internal/llama/sampling_ext.h
internal/llama/sampling_ext.cpp
internal/llama/build-info.cpp
internal/llama/build-info.cpp.in
internal/llama/README.md
internal/llama/.gitignore
internal/llama/llama.cpp/             ← 整个 vendored llama.cpp 源码
internal/llama/ggml/                  ← 整个 vendored ggml 源码
```

### 8.2 CMake 构建

```
CMakeLists.txt
CMakePresets.json
build/                                ← 整个构建产物目录
```

### 8.3 Build-tag 切换文件

```
internal/localmodel/model_backend_nocgo.go
internal/localmodel/metadata_nollamacpp.go
```

---

## 9. 分步实施计划

### Phase 0：准备（预计 0.5 天）

- [ ] `go get github.com/ebitengine/purego`
- [ ] 创建 `internal/llamadl/` 目录
- [ ] 手动下载一个 macOS arm64 release 包，检查实际文件结构和库名
- [ ] 编写一个最小 PoC：用 purego dlopen `libllama.dylib`，调用 `llama_backend_init` 确认可行

### Phase 1：基础绑定层（预计 2 天）

- [ ] 实现 `llamadl/types.go` — 所有 C ABI 结构体
- [ ] 实现 `llamadl/library.go` — dlopen + 全部函数注册
- [ ] 实现 `llamadl/errors.go`
- [ ] 实现 `llamadl/platform.go`
- [ ] 编写结构体大小/偏移验证测试（用 CGO 辅助程序输出 C 端的 sizeof/offsetof）

### Phase 2：核心类型封装（预计 2 天）

- [ ] 实现 `llamadl/model.go` — Model
- [ ] 实现 `llamadl/context.go` — Context（含 abort callback）
- [ ] 实现 `llamadl/batch.go` — Batch
- [ ] 实现 `llamadl/gguf.go` — GetModelArch / GGUF 读取
- [ ] 实现 `llamadl/gpu.go` — EnumerateGPUs
- [ ] 编写单元测试：加载模型、创建上下文、tokenize、decode

### Phase 3：采样器与语法（预计 1.5 天）

- [ ] 实现 `llamadl/sampler.go` — SamplingContext（采样链）
- [ ] 实现 `llamadl/grammar.go` — Grammar（用官方 sampler API）
- [ ] 实现 `llamadl/schema.go` — SchemaToGrammar（纯 Go 实现）
- [ ] 编写测试：采样、grammar 约束生成、JSON Schema → GBNF

### Phase 4：多模态（预计 1 天）

- [ ] 实现 `llamadl/mtmd.go` — MtmdContext + MultimodalTokenize
- [ ] 处理 `mtmd_helper_bitmap_init_from_buf` 的 Go 替代（image 解码）
- [ ] 处理 `mtmd_input_text` 结构体的 Go 构造

### Phase 5：下载管理（预计 1 天）

- [ ] 实现 `llamadl/download.go` — 下载 + 解压 + 版本管理
- [ ] 支持代理（复用现有 DynamicHTTPClient）
- [ ] 支持进度回调
- [ ] 测试各平台下载 URL 拼接

### Phase 6：适配上层代码（预计 1.5 天）

- [ ] 修改 `llamawrapper/engine.go` — 替换 import 和类型引用
- [ ] 修改 `llamawrapper/completion.go`
- [ ] 修改 `llamawrapper/handler.go`
- [ ] 修改 `llamawrapper/kvcache.go`
- [ ] 修改 `llamawrapper/sequence.go`
- [ ] 合并 `model_backend_cgo.go` → `model_backend_engine.go`，删除 build tag
- [ ] 合并 `metadata_llamacpp.go` → `metadata.go`，删除 build tag
- [ ] 删除 `model_backend_nocgo.go`、`metadata_nollamacpp.go`
- [ ] 更新 `go.mod`

### Phase 7：初始化流程改造（预计 0.5 天）

- [ ] 在 `engine.go` 的 `NewEngine` 中集成库下载检查和 dlopen 流程
- [ ] 确定 Library 生命周期管理（全局单例 vs 引擎持有）
- [ ] 添加 `KOCORT_LLAMA_LIB_DIR` 环境变量支持用户自定义库路径

### Phase 8：清理与删除（预计 0.5 天）

- [ ] 删除 `internal/llama/` 目录（全部）
- [ ] 删除 `CMakeLists.txt`、`CMakePresets.json`
- [ ] 删除 `build/` 目录
- [ ] 更新 `.gitignore`
- [ ] 更新 `scripts/build.sh`（不再需要 CMake 步骤）

### Phase 9：集成测试（预计 1 天）

- [ ] 端到端测试：下载库 → 加载模型 → chat completion → 验证输出
- [ ] 测试 Grammar 约束生成
- [ ] 测试 Embedding
- [ ] 测试多模态（如果有测试模型）
- [ ] 测试 abort/cancel 流程
- [ ] 测试 KV cache shift
- [ ] 跨平台测试（macOS, Linux, Windows）

---

## 10. 风险与测试策略

### 10.1 已知风险

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| purego 结构体对齐不一致 | 段错误/数据损坏 | 编写 C 辅助程序输出 sizeof/offsetof，Go 测试中断言匹配 |
| purego 不支持返回大结构体 | 无法调用 `_default_params` | 使用 `SyscallN` 手动调用，通过隐式指针返回 |
| purego 回调函数跨平台差异 | abort callback 不工作 | 各平台单独测试；purego 的 `NewCallback` 在 darwin/linux/windows 都有支持 |
| llama.cpp API 版本演进 | 未来版本结构体布局变化 | 锁定 `LlamaCppVersion` 常量，升级时逐项验证 |
| `schema_to_grammar` 纯 Go 实现不完整 | 复杂 JSON Schema 解析失败 | 先实现常用子集，逐步补全；添加回归测试 |
| `mtmd_helper_*` 内联函数不可用 | 多模态功能异常 | 用 Go image 库替代图片解码，手动构造结构体 |
| 下载时网络不可用 | 首次启动失败 | 支持离线模式（手动放置库文件）；提供清晰错误信息 |
| GPU 后端动态模块路径 | GPU 加速不生效 | 调用 `ggml_backend_load_all_from_path(libDir)` 确保后端被发现 |

### 10.2 测试策略

#### 10.2.1 本地测试环境

集成测试依赖以下本地资源，**不需要网络下载**：

| 资源 | 路径 | 说明 |
|------|------|------|
| 官方预构建库 | `/Users/libi/Downloads/llama-b8721/` | 已验证 b8721 macOS x86_64 release，含全部所需 dylib |
| 测试模型 | `~/.kocort/models/gemma-4-E2B-it-Q4_K_M.gguf` | Gemma 4 E2B-IT Q4_K_M (3.2GB) |

环境变量配置（在 `.env.test` 或直接 export）：

```bash
# 指向已下载的 release 包目录
export KOCORT_LLAMA_LIB_DIR=/Users/libi/Downloads/llama-b8721

# 模型目录
export KOCORT_MODELS_DIR=$HOME/.kocort/models
```

#### 10.2.2 单元测试（不需要模型/库文件）

文件：`internal/llamadl/*_test.go`

| 测试项 | 文件 | 说明 |
|--------|------|------|
| 结构体大小/偏移 | `types_test.go` | `unsafe.Sizeof(cModelParams{})` 断言与 C 端一致 |
| JSON Schema → GBNF | `schema_test.go` | 覆盖 string/number/object/array/enum/oneOf 等 |
| 下载 URL 拼接 | `download_test.go` | 各 OS/Arch/GPU 组合验证 URL 格式 |
| 平台检测 | `platform_test.go` | `LibName("llama")` 返回正确文件名 |

运行方式：
```bash
go test ./internal/llamadl/... -run 'Test(Types|Schema|Download|Platform)' -v
```

#### 10.2.3 集成测试（需要库文件 + 模型）

文件：`internal/llamadl/integration_test.go`（使用 `//go:build integration` 标签隔离）

**前置条件检查**（每个集成测试的 `TestMain` 统一检查）：
```go
func TestMain(m *testing.M) {
    libDir := os.Getenv("KOCORT_LLAMA_LIB_DIR")
    if libDir == "" {
        fmt.Println("SKIP: KOCORT_LLAMA_LIB_DIR not set")
        os.Exit(0)
    }
    modelsDir := os.Getenv("KOCORT_MODELS_DIR")
    if modelsDir == "" {
        modelsDir = filepath.Join(os.Getenv("HOME"), ".kocort", "models")
    }
    modelPath := filepath.Join(modelsDir, "gemma-4-E2B-it-Q4_K_M.gguf")
    if _, err := os.Stat(modelPath); err != nil {
        fmt.Printf("SKIP: model not found at %s\n", modelPath)
        os.Exit(0)
    }
    os.Exit(m.Run())
}
```

**测试用例清单**：

| # | 测试函数 | 验证内容 | 预期 |
|---|----------|----------|------|
| 1 | `TestLibraryOpen` | dlopen 全部 dylib，注册函数不 panic | Library 非 nil，Close 不报错 |
| 2 | `TestBackendInit` | `llama_backend_init()` 调用成功 | 不 panic |
| 3 | `TestBackendLoadGPU` | `ggml_backend_load_all_from_path(libDir)` 加载后端 | `ggml_backend_dev_count() >= 1`（至少 CPU） |
| 4 | `TestGetModelArch` | 读取 GGUF 元数据中的 `general.architecture` | 返回 `"gemma4"` |
| 5 | `TestLoadModel` | `llama_model_load_from_file` 加载模型 | model ptr 非 0，`model_n_embd > 0` |
| 6 | `TestCreateContext` | 创建推理上下文（n_ctx=2048, n_batch=512） | ctx ptr 非 0，`llama_n_ctx == 2048` |
| 7 | `TestTokenize` | 对 `"Hello, world!"` 进行 tokenize | token 数 > 0，roundtrip detokenize 匹配 |
| 8 | `TestTokenToPiece` | 遍历 token 0..100，`token_to_piece` 不 panic | 全部返回非空字符串 |
| 9 | `TestBatchDecodeSmall` | 创建 batch、添加 token、decode | `llama_decode` 返回 0（成功） |
| 10 | `TestGetLogits` | decode 后调用 `llama_get_logits_ith(ctx, 0)` | 返回非 nil float 指针，长度 == vocab_size |
| 11 | `TestSamplerChain` | 构建完整采样链（top_k=40, temp=0.8, dist） | `llama_sampler_sample` 返回有效 token ID |
| 12 | `TestSamplerGreedy` | 构建 greedy 采样链（temp=0） | 采样结果确定性（连续两次相同） |
| 13 | `TestGrammarJSON` | 用 `llama_sampler_init_grammar` 约束 JSON 输出 | 生成的文本是合法 JSON |
| 14 | `TestSchemaToGrammar` | 传入 `{"type":"object","properties":{"name":{"type":"string"}}}` | 返回有效 GBNF 字符串 |
| 15 | `TestKvCacheOps` | seq_rm → seq_add → seq_cp → can_shift | 全部操作不 panic |
| 16 | `TestAbortCallback` | 设置 abort callback 在第 1 个 token 后中止 | `llama_decode` 返回中止码 |
| 17 | `TestChatCompletion` | 端到端：load → ctx → tokenize prompt → decode loop → 采样 20 tokens | 输出可读文本，token 数 == 20 |
| 18 | `TestEmbedding` | 创建 embedding 上下文（embeddings=true），获取向量 | `get_embeddings_seq` 返回非零向量，维度 == n_embd |
| 19 | `TestModelFreeCleanup` | load → free → 再次 load | 无内存泄漏/崩溃 |
| 20 | `TestDefaultParams` | 调用 `llama_model_default_params` / `llama_context_default_params` | 返回结构体字段值合理（n_gpu_layers=99, n_ctx=0） |

运行方式：
```bash
# 运行全部集成测试
KOCORT_LLAMA_LIB_DIR=/Users/libi/Downloads/llama-b8721 \
  go test ./internal/llamadl/... -tags integration -v -timeout 300s

# 只运行单个测试
KOCORT_LLAMA_LIB_DIR=/Users/libi/Downloads/llama-b8721 \
  go test ./internal/llamadl/... -tags integration -run TestChatCompletion -v
```

#### 10.2.4 llamawrapper 层集成测试

文件：`internal/localmodel/llamawrapper/integration_test.go`

验证上层封装在切换到 `llamadl` 后仍然正常工作：

| # | 测试函数 | 验证内容 |
|---|----------|----------|
| 1 | `TestEngineLoad` | Engine.load() 成功加载模型 |
| 2 | `TestEngineChatCompletion` | ChatCompletion 返回流式 token |
| 3 | `TestEngineTextCompletion` | TextCompletion 返回文本 |
| 4 | `TestEngineEmbedding` | Embedding 返回向量 |
| 5 | `TestEngineGrammar` | 带 JSON Schema 的 ChatCompletion 返回合法 JSON |
| 6 | `TestEngineAbort` | 请求 abort 后 completion 正常终止 |
| 7 | `TestEngineSequentialRequests` | 连续 3 次 ChatCompletion（验证 KV cache 复用） |

运行方式：
```bash
KOCORT_LLAMA_LIB_DIR=/Users/libi/Downloads/llama-b8721 \
  go test ./internal/localmodel/llamawrapper/... -tags integration -v -timeout 600s
```

#### 10.2.5 性能基准测试

文件：`internal/llamadl/bench_test.go`

```go
func BenchmarkTokenize(b *testing.B)      { /* 1000 字符文本 tokenize */ }
func BenchmarkDecode(b *testing.B)        { /* 单次 batch decode */ }
func BenchmarkSamplerSample(b *testing.B) { /* 单次采样 */ }
func BenchmarkFullGeneration(b *testing.B) { /* 生成 100 tokens */ }
```

运行方式：
```bash
KOCORT_LLAMA_LIB_DIR=/Users/libi/Downloads/llama-b8721 \
  go test ./internal/llamadl/... -tags integration -bench=. -benchmem -count=3
```

重点关注 purego FFI 调用开销是否可忽略（预期 < 1μs/call，远小于单次 decode 的 ~10ms）。

#### 10.2.6 CI 环境说明

CI 中集成测试需要：
1. 预先下载对应平台的 llama.cpp release 包并缓存
2. 准备一个小模型文件（建议 CI 用 TinyLlama 1.1B Q4，~600MB，比 Gemma 4 小）
3. 设置 `KOCORT_LLAMA_LIB_DIR` 和 `KOCORT_MODELS_DIR` 环境变量
4. 单元测试无需上述依赖，可直接运行

```yaml
# GitHub Actions 示例
- name: Run unit tests
  run: go test ./internal/llamadl/... -v

- name: Run integration tests
  if: runner.os == 'macOS'  # 或 Linux
  env:
    KOCORT_LLAMA_LIB_DIR: ${{ github.workspace }}/.cache/llama-libs
    KOCORT_MODELS_DIR: ${{ github.workspace }}/.cache/models
  run: |
    go test ./internal/llamadl/... -tags integration -v -timeout 300s
    go test ./internal/localmodel/llamawrapper/... -tags integration -v -timeout 600s
```

### 10.3 回滚计划

在 Phase 6 之前，`internal/llama/` 和 `internal/llamadl/` 可以共存。如果发现方案不可行，只需删除 `internal/llamadl/` 目录即可回滚。

建议在独立的 feature branch 上开发，完成全部测试后再合并。

---

## 附录 A：purego 使用参考

```go
// 基础用法
lib, err := purego.Dlopen("libllama.dylib", purego.RTLD_LAZY)
if err != nil { panic(err) }

var llamaBackendInit func()
purego.RegisterLibFunc(&llamaBackendInit, lib, "llama_backend_init")
llamaBackendInit()

// 带参数和返回值
var llamaModelLoadFromFile func(path *byte, params uintptr) uintptr
purego.RegisterLibFunc(&llamaModelLoadFromFile, lib, "llama_model_load_from_file")

// 回调函数
callback := purego.NewCallback(func(progress float32, userData uintptr) int32 {
    fmt.Printf("Progress: %.1f%%\n", progress*100)
    return 1 // continue
})

// 对于返回大结构体的函数，使用 SyscallN
// C ABI: 大结构体通过隐式第一个参数（指向返回值的指针）传递
var pModelDefaultParams uintptr
purego.RegisterLibFunc(&pModelDefaultParams, lib, "llama_model_default_params")
// 实际调用需要传递目标指针
```

## 附录 B：版本锁定建议

```go
// internal/llamadl/version.go
const (
    MinLlamaCppVersion = "b8720"
    MaxLlamaCppVersion = "b9999" // 上限需要测试后确定
)
```

当用户自行提供库文件时，可以通过 `llama_print_system_info()` 获取版本字符串进行兼容性检查。

## 附录 C：环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `KOCORT_LLAMA_LIB_DIR` | 自定义库文件目录（跳过下载） | 空 |
| `KOCORT_LLAMA_VERSION` | 指定下载版本 | `b8720` |
| `KOCORT_LLAMA_GPU` | GPU 类型 (`cpu`/`vulkan`/`cuda-12.4`/`cuda-13.1`/`rocm-7.2`) | 自动检测 |
| `KOCORT_LIBRARY_PATH` | 兼容现有 GPU 后端搜索路径 | 保持现有行为 |
