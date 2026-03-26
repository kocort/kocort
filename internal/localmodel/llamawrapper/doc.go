// package llamawrapper is a standalone local LLM inference library built on top
// of llama.cpp via the internal/llama CGo bindings.
//
// It provides:
//   - GGUF model loading with GPU offloading and flash attention support
//   - Batched token inference with parallel sequence management
//   - KV cache with prefix reuse and context shifting
//   - Full OpenAI-compatible HTTP API (chat completions, completions, embeddings, models)
//   - Streaming (SSE) and non-streaming response modes
//   - Structured output via JSON schema / GBNF grammar constraints
//   - <think>...</think> reasoning block parsing
//   - <tool_call>...</tool_call> function calling support
//   - ChatML and Qwen3.5-native prompt templates
//   - Multimodal (vision) input via mtmd
//
// Quick start:
//
//	srv, err := localmodel.NewServer(localmodel.ServerConfig{
//	    Addr:      "127.0.0.1:8080",
//	    ModelPath: "/path/to/model.gguf",
//	})
//	if err != nil { log.Fatal(err) }
//	defer srv.Stop()
//	srv.Start() // blocks until stopped
//
// Or use the Engine directly for programmatic inference:
//
//	eng, err := localmodel.NewEngine(localmodel.EngineConfig{
//	    ModelPath:   "/path/to/model.gguf",
//	    ContextSize: 4096,
//	    Threads:     8,
//	    GPULayers:   99,
//	})
//	if err != nil { log.Fatal(err) }
//	defer eng.Close()
//
//	ch, err := eng.ChatCompletion(ctx, localmodel.ChatCompletionRequest{
//	    Messages: []localmodel.ChatMessage{{Role: "user", Content: "Hello!"}},
//	    Stream:   true,
//	})
package llamawrapper
