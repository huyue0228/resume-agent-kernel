The OpenAI tiktoken vocabularies are embedded from:

- https://openaipublic.blob.core.windows.net/encodings/cl100k_base.tiktoken
- https://openaipublic.blob.core.windows.net/encodings/o200k_base.tiktoken

No runtime download is allowed.

The vocabulary is used with `github.com/pkoukk/tiktoken-go` (MIT licensed).
Known modern model families use o200k; other models use cl100k as a baseline
estimate. Other model tokenizers may differ; the task
counter calibrates its estimate using reported input usage.
