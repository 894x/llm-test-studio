# Third-party notices

LLM Test Studio is licensed under Apache-2.0. That license does not replace the licenses of third-party packages, fonts, frameworks, or operating-system components used by the project.

Exact dependency versions are recorded in `go.mod`, `go.sum`, `apps/desktop/frontend/package.json`, and `apps/desktop/frontend/pnpm-lock.yaml`. The lists below are an initial inventory of direct dependencies and redistributed assets; transitive dependencies remain governed by their own license files and package metadata.

## Go dependencies

| Dependency | License | Source |
| --- | --- | --- |
| Wails v2 | MIT | <https://github.com/wailsapp/wails> |
| zalando/go-keyring | MIT | <https://github.com/zalando/go-keyring> |
| golang.org/x/sys | BSD-3-Clause | <https://pkg.go.dev/golang.org/x/sys> |
| modernc.org/sqlite | BSD-3-Clause | <https://pkg.go.dev/modernc.org/sqlite> |

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
