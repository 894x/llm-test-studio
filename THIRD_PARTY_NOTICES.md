# Third-party notices

LLM Test Studio is licensed under Apache-2.0. That license does not replace the licenses of third-party packages, fonts, frameworks, or operating-system components used by the project.

Exact dependency versions are recorded in `go.mod`, `go.sum`, `apps/desktop/frontend/package.json`, `apps/desktop/frontend/pnpm-lock.yaml`, and `internal/benchmarkruntime/uv.lock`. The lists below are an initial inventory of direct dependencies and redistributed assets; transitive dependencies remain governed by their own license files and package metadata.

## Go dependencies

| Dependency | License | Source |
| --- | --- | --- |
| Wails v2 | MIT | <https://github.com/wailsapp/wails> |
| zalando/go-keyring | MIT | <https://github.com/zalando/go-keyring> |
| golang.org/x/sys | BSD-3-Clause | <https://pkg.go.dev/golang.org/x/sys> |
| modernc.org/sqlite | BSD-3-Clause | <https://pkg.go.dev/modernc.org/sqlite> |

## Optional dataset evaluation runtime

The application ships a Python worker and a dependency lockfile. Preparing the evaluation environment downloads these packages; it does not change their licenses or the licenses and access conditions of individual datasets.

| Dependency | License | Source |
| --- | --- | --- |
| Inspect AI | MIT | <https://github.com/UKGovernmentBEIS/inspect_ai> |
| Inspect Evals | MIT | <https://github.com/UKGovernmentBEIS/inspect_evals> |
| OpenAI Python SDK | Apache-2.0 | <https://github.com/openai/openai-python> |
| Anthropic Python SDK | Apache-2.0 | <https://github.com/anthropics/anthropic-sdk-python> |

## Frontend dependencies and assets

| Dependency | License | Source |
| --- | --- | --- |
| @base-ui/react | MIT | <https://github.com/mui/base-ui> |
| @fontsource-variable/geist | OFL-1.1 | <https://fontsource.org/fonts/geist> |
| class-variance-authority | Apache-2.0 | <https://github.com/joe-bell/cva> |
| clsx | MIT | <https://github.com/lukeed/clsx> |
| html-to-image | MIT | <https://github.com/bubkoo/html-to-image> |
| jspdf | MIT | <https://github.com/parallax/jsPDF> |
| lucide-react | ISC | <https://github.com/lucide-icons/lucide> |
| radix-ui | MIT | <https://github.com/radix-ui/primitives> |
| react | MIT | <https://github.com/facebook/react> |
| react-dom | MIT | <https://github.com/facebook/react> |
| shadcn | MIT | <https://github.com/shadcn-ui/ui> |
| tailwind-merge | MIT | <https://github.com/dcastil/tailwind-merge> |
| tw-animate-css | MIT | <https://github.com/Wombosvideo/tw-animate-css> |

When redistributing application binaries, preserve all notices required by these licenses. Release archives include this file, `LICENSE`, and `NOTICE` as sidecar files.

## Kimi Vendor Verifier fixture

The `KVV-prompt-tokens-assistant-hello-stream` built-in Case includes an adapted
request and expected token baseline from
[Kimi-Vendor-Verifier](https://github.com/MoonshotAI/Kimi-Vendor-Verifier/tree/66092cf444c97356c0e11c5078c67116390615d9).

```text
MIT License

Copyright (c) 2026 Moonshot AI

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
