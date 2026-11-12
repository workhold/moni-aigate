# Dependency provenance and licenses

Checked: 2026-11-12. Sources: go.mod/go.sum, `go list -m -json all`, LICENSE files from downloaded Go modules, web/package-lock.json, installed npm package.json metadata, and the planned New API/Sub2API integration records. This inventory is not a vulnerability scan or a complete legal review.

The application, migrations, console and tests currently present in this checkout were written against the moni-aigate SDD contracts. New API/Sub2API are now allowed as external channels and may be imported, forked or vendored in later migration slices when their source and license records are added. Any imported component must retain its original LICENSE/NOTICE, source commit and local modification record; this inventory is the gate for that addition. General-purpose third-party libraries below remain subject to their own licenses. The owner has not selected the license for moni-aigate itself.

## Planned upstream components

| Component | Current mode | Source/commit record | License/NOTICE handling |
|---|---|---|---|
| New API | External relay channel (`channel_kind=new_api`) | [official repository](https://github.com/QuantumNous/new-api); exact commit is frozen per qualification slice | Preserve upstream LICENSE, NOTICE, UI attribution and applicable AGPL source-offer obligations before vendoring or distributing a modified service |
| Sub2API | External relay channel (`channel_kind=sub2api`) | [official repository](https://github.com/Wei-Shaw/sub2api); exact commit is frozen per qualification slice | Preserve upstream license/NOTICE and any network-service/source-disclosure obligations before vendoring or distributing a modified service |

The rows above are planned provenance entries, not proof that either repository is currently part of the aigate binary. A future `third_party/` or component directory must add the exact checkout, checksum, license files and modification log before source integration is merged.

## Direct dependencies

| Ecosystem | Dependency | Version | License evidence |
|---|---|---|---|
| Go | [github.com/gin-gonic/gin](https://pkg.go.dev/github.com/gin-gonic/gin@v1.11.0) | v1.11.0 | MIT; module root LICENSE |
| Go | [github.com/google/uuid](https://pkg.go.dev/github.com/google/uuid@v1.6.0) | v1.6.0 | BSD-3-Clause; module root LICENSE |
| Go | [github.com/jackc/pgx/v5](https://pkg.go.dev/github.com/jackc/pgx/v5@v5.7.6) | v5.7.6 | MIT; module root LICENSE |
| Go | [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto@v0.43.0) | v0.43.0 | BSD-3-Clause; module root LICENSE |
| Go | [golang.org/x/time](https://pkg.go.dev/golang.org/x/time@v0.14.0) | v0.14.0 | BSD-3-Clause; module root LICENSE |
| npm | [@element-plus/icons-vue](https://www.npmjs.com/package/@element-plus/icons-vue/v/2.3.2) | 2.3.2 | MIT; npm metadata |
| npm | [element-plus](https://www.npmjs.com/package/element-plus/v/2.11.8) | 2.11.8 | MIT; npm metadata |
| npm | [qrcode](https://www.npmjs.com/package/qrcode/v/1.5.4) | 1.5.4 | MIT; installed LICENSE and npm metadata; local browser QR generation |
| npm | [@types/qrcode](https://www.npmjs.com/package/@types/qrcode/v/1.5.6) | 1.5.6 | MIT; installed LICENSE and npm metadata; build only |
| npm | [vue](https://www.npmjs.com/package/vue/v/3.5.24) | 3.5.24 | MIT; npm metadata |
| npm | [vue-router](https://www.npmjs.com/package/vue-router/v/4.6.3) | 4.6.3 | MIT; npm metadata |
| npm | [@vitejs/plugin-vue](https://www.npmjs.com/package/@vitejs/plugin-vue/v/6.0.2) | 6.0.2 | MIT; npm metadata |
| npm | [typescript](https://www.npmjs.com/package/typescript/v/5.9.3) | 5.9.3 | Apache-2.0; npm metadata |
| npm | [vite](https://www.npmjs.com/package/vite/v/7.2.2) | 7.2.2 | MIT; npm metadata |
| npm | [vue-tsc](https://www.npmjs.com/package/vue-tsc/v/3.1.4) | 3.1.4 | MIT; npm metadata |

## Full Go module graph

Includes direct, transitive, test and optional modules. Graph membership does not prove inclusion in a particular platform binary. Missing local source is explicitly marked unverified. Multiple license identifiers mean multiple texts were found, not a blanket choice between them.

| Module | Version | Direct | Local license evidence |
|---|---|---|---|
| [github.com/beorn7/perks](https://pkg.go.dev/github.com/beorn7/perks@v1.0.1) | v1.0.1 | false | Source not downloaded; not verified |
| [github.com/bytedance/sonic](https://pkg.go.dev/github.com/bytedance/sonic@v1.14.0) | v1.14.0 | false | Apache-2.0; LICENSE |
| [github.com/bytedance/sonic/loader](https://pkg.go.dev/github.com/bytedance/sonic/loader@v0.3.0) | v0.3.0 | false | Apache-2.0; LICENSE |
| [github.com/cespare/xxhash/v2](https://pkg.go.dev/github.com/cespare/xxhash/v2@v2.2.0) | v2.2.0 | false | Source not downloaded; not verified |
| [github.com/cloudwego/base64x](https://pkg.go.dev/github.com/cloudwego/base64x@v0.1.6) | v0.1.6 | false | Apache-2.0; LICENSE, LICENSE-APACHE |
| [github.com/davecgh/go-spew](https://pkg.go.dev/github.com/davecgh/go-spew@v1.1.1) | v1.1.1 | false | ISC; LICENSE |
| [github.com/francoispqt/gojay](https://pkg.go.dev/github.com/francoispqt/gojay@v1.2.13) | v1.2.13 | false | Source not downloaded; not verified |
| [github.com/gabriel-vasile/mimetype](https://pkg.go.dev/github.com/gabriel-vasile/mimetype@v1.4.8) | v1.4.8 | false | MIT; LICENSE |
| [github.com/gin-contrib/sse](https://pkg.go.dev/github.com/gin-contrib/sse@v1.1.0) | v1.1.0 | false | MIT; LICENSE |
| [github.com/gin-gonic/gin](https://pkg.go.dev/github.com/gin-gonic/gin@v1.11.0) | v1.11.0 | true | MIT; LICENSE |
| [github.com/go-playground/assert/v2](https://pkg.go.dev/github.com/go-playground/assert/v2@v2.2.0) | v2.2.0 | false | MIT; LICENSE |
| [github.com/go-playground/locales](https://pkg.go.dev/github.com/go-playground/locales@v0.14.1) | v0.14.1 | false | MIT; LICENSE |
| [github.com/go-playground/universal-translator](https://pkg.go.dev/github.com/go-playground/universal-translator@v0.18.1) | v0.18.1 | false | MIT; LICENSE |
| [github.com/go-playground/validator/v10](https://pkg.go.dev/github.com/go-playground/validator/v10@v10.27.0) | v10.27.0 | false | MIT; LICENSE |
| [github.com/goccy/go-json](https://pkg.go.dev/github.com/goccy/go-json@v0.10.2) | v0.10.2 | false | MIT; LICENSE |
| [github.com/goccy/go-yaml](https://pkg.go.dev/github.com/goccy/go-yaml@v1.18.0) | v1.18.0 | false | MIT; LICENSE |
| [github.com/golang/protobuf](https://pkg.go.dev/github.com/golang/protobuf@v1.5.0) | v1.5.0 | false | Source not downloaded; not verified |
| [github.com/google/go-cmp](https://pkg.go.dev/github.com/google/go-cmp@v0.7.0) | v0.7.0 | false | BSD-3-Clause; LICENSE |
| [github.com/google/gofuzz](https://pkg.go.dev/github.com/google/gofuzz@v1.0.0) | v1.0.0 | false | Source not downloaded; not verified |
| [github.com/google/uuid](https://pkg.go.dev/github.com/google/uuid@v1.6.0) | v1.6.0 | true | BSD-3-Clause; LICENSE |
| [github.com/jackc/pgpassfile](https://pkg.go.dev/github.com/jackc/pgpassfile@v1.0.0) | v1.0.0 | false | MIT; LICENSE |
| [github.com/jackc/pgservicefile](https://pkg.go.dev/github.com/jackc/pgservicefile@v0.0.0-20240606120523-5a60cdf6a761) | v0.0.0-20240606120523-5a60cdf6a761 | false | MIT; LICENSE |
| [github.com/jackc/pgx/v5](https://pkg.go.dev/github.com/jackc/pgx/v5@v5.7.6) | v5.7.6 | true | MIT; LICENSE |
| [github.com/jackc/puddle/v2](https://pkg.go.dev/github.com/jackc/puddle/v2@v2.2.2) | v2.2.2 | false | MIT; LICENSE |
| [github.com/json-iterator/go](https://pkg.go.dev/github.com/json-iterator/go@v1.1.12) | v1.1.12 | false | MIT; LICENSE |
| [github.com/klauspost/cpuid/v2](https://pkg.go.dev/github.com/klauspost/cpuid/v2@v2.3.0) | v2.3.0 | false | MIT; LICENSE |
| [github.com/kr/pretty](https://pkg.go.dev/github.com/kr/pretty@v0.3.0) | v0.3.0 | false | Source not downloaded; not verified |
| [github.com/leodido/go-urn](https://pkg.go.dev/github.com/leodido/go-urn@v1.4.0) | v1.4.0 | false | MIT; LICENSE |
| [github.com/mattn/go-isatty](https://pkg.go.dev/github.com/mattn/go-isatty@v0.0.20) | v0.0.20 | false | MIT; LICENSE |
| [github.com/modern-go/concurrent](https://pkg.go.dev/github.com/modern-go/concurrent@v0.0.0-20180228061459-e0a39a4cb421) | v0.0.0-20180228061459-e0a39a4cb421 | false | Apache-2.0; LICENSE |
| [github.com/modern-go/reflect2](https://pkg.go.dev/github.com/modern-go/reflect2@v1.0.2) | v1.0.2 | false | Apache-2.0; LICENSE |
| [github.com/pelletier/go-toml/v2](https://pkg.go.dev/github.com/pelletier/go-toml/v2@v2.2.4) | v2.2.4 | false | MIT; LICENSE |
| [github.com/pmezard/go-difflib](https://pkg.go.dev/github.com/pmezard/go-difflib@v1.0.0) | v1.0.0 | false | BSD-2-Clause; LICENSE |
| [github.com/prometheus/client_golang](https://pkg.go.dev/github.com/prometheus/client_golang@v1.19.1) | v1.19.1 | false | Source not downloaded; not verified |
| [github.com/prometheus/client_model](https://pkg.go.dev/github.com/prometheus/client_model@v0.5.0) | v0.5.0 | false | Source not downloaded; not verified |
| [github.com/prometheus/common](https://pkg.go.dev/github.com/prometheus/common@v0.48.0) | v0.48.0 | false | Source not downloaded; not verified |
| [github.com/prometheus/procfs](https://pkg.go.dev/github.com/prometheus/procfs@v0.12.0) | v0.12.0 | false | Source not downloaded; not verified |
| [github.com/quic-go/qpack](https://pkg.go.dev/github.com/quic-go/qpack@v0.5.1) | v0.5.1 | false | MIT; LICENSE.md |
| [github.com/quic-go/quic-go](https://pkg.go.dev/github.com/quic-go/quic-go@v0.54.0) | v0.54.0 | false | MIT; LICENSE |
| [github.com/stretchr/objx](https://pkg.go.dev/github.com/stretchr/objx@v0.5.0) | v0.5.0 | false | Source not downloaded; not verified |
| [github.com/stretchr/testify](https://pkg.go.dev/github.com/stretchr/testify@v1.11.1) | v1.11.1 | false | MIT; LICENSE |
| [github.com/twitchyliquid64/golang-asm](https://pkg.go.dev/github.com/twitchyliquid64/golang-asm@v0.15.1) | v0.15.1 | false | BSD-3-Clause; LICENSE |
| [github.com/ugorji/go/codec](https://pkg.go.dev/github.com/ugorji/go/codec@v1.3.0) | v1.3.0 | false | MIT; LICENSE |
| [github.com/yuin/goldmark](https://pkg.go.dev/github.com/yuin/goldmark@v1.4.13) | v1.4.13 | false | Source not downloaded; not verified |
| [go.uber.org/mock](https://pkg.go.dev/go.uber.org/mock@v0.5.0) | v0.5.0 | false | Apache-2.0; LICENSE |
| [golang.org/x/arch](https://pkg.go.dev/golang.org/x/arch@v0.20.0) | v0.20.0 | false | BSD-3-Clause; LICENSE |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto@v0.43.0) | v0.43.0 | true | BSD-3-Clause; LICENSE |
| [golang.org/x/mod](https://pkg.go.dev/golang.org/x/mod@v0.28.0) | v0.28.0 | false | BSD-3-Clause; LICENSE |
| [golang.org/x/net](https://pkg.go.dev/golang.org/x/net@v0.45.0) | v0.45.0 | false | BSD-3-Clause; LICENSE |
| [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync@v0.17.0) | v0.17.0 | false | BSD-3-Clause; LICENSE |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys@v0.37.0) | v0.37.0 | false | BSD-3-Clause; LICENSE |
| [golang.org/x/telemetry](https://pkg.go.dev/golang.org/x/telemetry@v0.0.0-20250908211612-aef8a434d053) | v0.0.0-20250908211612-aef8a434d053 | false | Source not downloaded; not verified |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term@v0.36.0) | v0.36.0 | false | Source not downloaded; not verified |
| [golang.org/x/text](https://pkg.go.dev/golang.org/x/text@v0.30.0) | v0.30.0 | false | BSD-3-Clause; LICENSE |
| [golang.org/x/time](https://pkg.go.dev/golang.org/x/time@v0.14.0) | v0.14.0 | true | BSD-3-Clause; LICENSE |
| [golang.org/x/tools](https://pkg.go.dev/golang.org/x/tools@v0.37.0) | v0.37.0 | false | BSD-3-Clause; LICENSE |
| [golang.org/x/xerrors](https://pkg.go.dev/golang.org/x/xerrors@v0.0.0-20191204190536-9bdfabe68543) | v0.0.0-20191204190536-9bdfabe68543 | false | Source not downloaded; not verified |
| [google.golang.org/protobuf](https://pkg.go.dev/google.golang.org/protobuf@v1.36.9) | v1.36.9 | false | BSD-3-Clause; LICENSE |
| [gopkg.in/check.v1](https://pkg.go.dev/gopkg.in/check.v1@v1.0.0-20201130134442-10cb98267c6c) | v1.0.0-20201130134442-10cb98267c6c | false | Source not downloaded; not verified |
| [gopkg.in/yaml.v3](https://pkg.go.dev/gopkg.in/yaml.v3@v3.0.1) | v3.0.1 | false | Apache-2.0 + MIT; LICENSE |
| [rsc.io/pdf](https://pkg.go.dev/rsc.io/pdf@v0.1.1) | v0.1.1 | false | Source not downloaded; not verified |

## Locked npm packages

License declarations come from the lockfile and installed package.json; every npm LICENSE attachment has not been manually reviewed. Optional platform packages and build tools are not all shipped in the browser bundle.

| Package | Version | Declared license | Scope |
|---|---|---|---|
| @babel/helper-string-parser | 7.29.11 | MIT | runtime dependency graph |
| @babel/helper-validator-identifier | 7.29.7 | MIT | runtime dependency graph |
| @babel/parser | 7.29.12 | MIT | runtime dependency graph |
| @babel/types | 7.29.11 | MIT | runtime dependency graph |
| @ctrl/tinycolor | 3.6.1 | MIT | runtime dependency graph |
| @element-plus/icons-vue | 2.3.2 | MIT | runtime dependency graph |
| @esbuild/aix-ppc64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/android-arm | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/android-arm64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/android-x64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/darwin-arm64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/darwin-x64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/freebsd-arm64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/freebsd-x64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/linux-arm | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/linux-arm64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/linux-ia32 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/linux-loong64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/linux-mips64el | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/linux-ppc64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/linux-riscv64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/linux-s390x | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/linux-x64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/netbsd-arm64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/netbsd-x64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/openbsd-arm64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/openbsd-x64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/openharmony-arm64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/sunos-x64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/win32-arm64 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/win32-ia32 | 0.25.12 | MIT | build/dev; optional platform |
| @esbuild/win32-x64 | 0.25.12 | MIT | build/dev; optional platform |
| @floating-ui/core | 1.8.0 | MIT | runtime dependency graph |
| @floating-ui/dom | 1.8.0 | MIT | runtime dependency graph |
| @floating-ui/utils | 0.2.12 | MIT | runtime dependency graph |
| @jridgewell/sourcemap-codec | 1.6.0 | MIT | runtime dependency graph |
| @napi-rs/lzma-linux-x64-gnu | 1.5.1 | MIT | build/dev; optional platform |
| @popperjs/core | 2.11.8 | MIT | runtime dependency graph |
| @rolldown/pluginutils | 1.0.0-beta.50 | MIT | build/dev |
| @rollup/rollup-android-arm-eabi | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-android-arm64 | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-darwin-arm64 | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-darwin-x64 | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-freebsd-arm64 | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-freebsd-x64 | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-arm-gnueabihf | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-arm-musleabihf | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-arm64-gnu | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-arm64-musl | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-loong64-gnu | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-loong64-musl | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-ppc64-gnu | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-ppc64-musl | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-riscv64-gnu | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-riscv64-musl | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-s390x-gnu | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-x64-gnu | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-linux-x64-musl | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-openbsd-x64 | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-openharmony-arm64 | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-win32-arm64-msvc | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-win32-ia32-msvc | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-win32-x64-gnu | 4.66.0 | MIT | build/dev; optional platform |
| @rollup/rollup-win32-x64-msvc | 4.66.0 | MIT | build/dev; optional platform |
| @types/estree | 1.0.9 | MIT | build/dev |
| @types/lodash | 4.17.27 | MIT | runtime dependency graph |
| @types/lodash-es | 4.17.12 | MIT | runtime dependency graph |
| @types/web-bluetooth | 0.0.16 | MIT | runtime dependency graph |
| @vitejs/plugin-vue | 6.0.2 | MIT | build/dev |
| @volar/language-core | 2.4.23 | MIT | build/dev |
| @volar/source-map | 2.4.23 | MIT | build/dev |
| @volar/typescript | 2.4.23 | MIT | build/dev |
| @vue/compiler-core | 3.5.24 | MIT | runtime dependency graph |
| @vue/compiler-dom | 3.5.24 | MIT | runtime dependency graph |
| @vue/compiler-sfc | 3.5.24 | MIT | runtime dependency graph |
| @vue/compiler-ssr | 3.5.24 | MIT | runtime dependency graph |
| @vue/devtools-api | 6.6.4 | MIT | runtime dependency graph |
| @vue/language-core | 3.1.4 | MIT | build/dev |
| @vue/reactivity | 3.5.24 | MIT | runtime dependency graph |
| @vue/runtime-core | 3.5.24 | MIT | runtime dependency graph |
| @vue/runtime-dom | 3.5.24 | MIT | runtime dependency graph |
| @vue/server-renderer | 3.5.24 | MIT | runtime dependency graph |
| @vue/shared | 3.5.24 | MIT | runtime dependency graph |
| @vueuse/core | 9.13.0 | MIT | runtime dependency graph |
| vue-demi | 0.14.10 | MIT | runtime dependency graph |
| @vueuse/metadata | 9.13.0 | MIT | runtime dependency graph |
| @vueuse/shared | 9.13.0 | MIT | runtime dependency graph |
| vue-demi | 0.14.10 | MIT | runtime dependency graph |
| alien-signals | 3.2.1 | MIT | build/dev |
| async-validator | 4.2.5 | MIT | runtime dependency graph |
| csstype | 3.2.3 | MIT | runtime dependency graph |
| dayjs | 1.11.23 | MIT | runtime dependency graph |
| element-plus | 2.11.8 | MIT | runtime dependency graph |
| entities | 4.5.0 | BSD-2-Clause | runtime dependency graph |
| esbuild | 0.25.12 | MIT | build/dev |
| estree-walker | 2.0.2 | MIT | runtime dependency graph |
| fdir | 6.5.0 | MIT | build/dev |
| fsevents | 2.3.3 | MIT | build/dev; optional platform |
| lodash | 4.18.1 | MIT | runtime dependency graph |
| lodash-es | 4.18.1 | MIT | runtime dependency graph |
| lodash-unified | 1.0.3 | MIT | runtime dependency graph |
| magic-string | 0.30.21 | MIT | runtime dependency graph |
| memoize-one | 6.0.0 | MIT | runtime dependency graph |
| muggle-string | 0.4.1 | MIT | build/dev |
| nanoid | 3.3.20 | MIT | runtime dependency graph |
| normalize-wheel-es | 1.2.0 | BSD-3-Clause | runtime dependency graph |
| path-browserify | 1.0.1 | MIT | build/dev |
| picocolors | 1.1.1 | ISC | runtime dependency graph |
| picomatch | 4.0.7 | MIT | build/dev |
| postcss | 8.5.32 | MIT | runtime dependency graph |
| rollup | 4.66.0 | MIT | build/dev |
| source-map-js | 1.2.2 | BSD-3-Clause | runtime dependency graph |
| tinyglobby | 0.2.17 | MIT | build/dev |
| typescript | 5.9.3 | Apache-2.0 | runtime dependency graph |
| vite | 7.2.2 | MIT | build/dev |
| vscode-uri | 3.2.0 | MIT | build/dev |
| vue | 3.5.24 | MIT | runtime dependency graph |
| vue-router | 4.6.3 | MIT | runtime dependency graph |
| vue-tsc | 3.1.4 | MIT | build/dev |
| @types/node | 26.6.6 | MIT | build/dev |
| @types/qrcode | 1.5.6 | MIT | build/dev |
| ansi-regex | 5.0.1 | MIT | runtime dependency graph |
| ansi-styles | 4.3.0 | MIT | runtime dependency graph |
| camelcase | 5.3.1 | MIT | runtime dependency graph |
| cliui | 6.0.0 | ISC | runtime dependency graph |
| color-convert | 2.0.1 | MIT | runtime dependency graph |
| color-name | 1.1.4 | MIT | runtime dependency graph |
| decamelize | 1.2.0 | MIT | runtime dependency graph |
| dijkstrajs | 1.0.3 | MIT | runtime dependency graph |
| emoji-regex | 8.0.0 | MIT | runtime dependency graph |
| find-up | 4.1.0 | MIT | runtime dependency graph |
| get-caller-file | 2.0.5 | ISC | runtime dependency graph |
| is-fullwidth-code-point | 3.0.0 | MIT | runtime dependency graph |
| locate-path | 5.0.0 | MIT | runtime dependency graph |
| p-limit | 2.3.0 | MIT | runtime dependency graph |
| p-locate | 4.1.0 | MIT | runtime dependency graph |
| p-try | 2.2.0 | MIT | runtime dependency graph |
| path-exists | 4.0.0 | MIT | runtime dependency graph |
| pngjs | 5.0.0 | MIT | runtime dependency graph |
| qrcode | 1.5.4 | MIT | runtime dependency graph |
| require-directory | 2.1.1 | MIT | runtime dependency graph |
| require-main-filename | 2.0.0 | ISC | runtime dependency graph |
| set-blocking | 2.0.0 | ISC | runtime dependency graph |
| string-width | 4.2.3 | MIT | runtime dependency graph |
| strip-ansi | 6.0.1 | MIT | runtime dependency graph |
| undici-types | 8.9.0 | MIT | build/dev |
| which-module | 2.0.1 | ISC | runtime dependency graph |
| wrap-ansi | 6.2.0 | MIT | runtime dependency graph |
| y18n | 4.0.3 | ISC | runtime dependency graph |
| yargs | 15.4.1 | MIT | runtime dependency graph |
| yargs-parser | 18.1.3 | ISC | runtime dependency graph |

## Distribution and remaining work

- Dockerfile uses Node 22 Alpine and Go 1.26 Alpine build stages, Alpine 3.23 runtime, and Compose uses PostgreSQL 16 Alpine. These image tags are not pinned to digests. Image OS packages, Go/Node toolchains and PostgreSQL have separate licensing; no complete container SBOM is claimed here.
- MIT/BSD/ISC redistribution requires retaining applicable copyright and license notices. Apache-2.0 components also require applicable NOTICE preservation and modification notices. Before binary/image publication, collect LICENSE/NOTICE from the final exact artifacts and review unverified items. This document is an inventory, not that complete notice bundle.
- Independent implementation does not grant rights to third-party trademarks/assets or remove all third-party obligations. API compatibility and design research should remain documented separately from imported code.
- Recheck after lockfile changes with `go list -m -json all`, `go version -m <built-binary>` and `npm ls --all --json`.
