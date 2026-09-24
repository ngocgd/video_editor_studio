# Research: Backend Architecture cho App Sinh Story Video + Pattern goclaw gọi Claude CLI

Ngày: 2026-09-24. Phạm vi: kiến trúc backend Go/Docker cho app local-first (RTX 5060 Ti 16GB, 32GB RAM, Windows 11) tiến hóa thành SaaS multi-tenant.

## 1. goclaw — cách gọi Claude CLI

### Repo
Không có repo "goclaw" gốc chính thức từ Anthropic/OpenClaw team — có nhiều fork cùng tên (`nextlevelbuilder/goclaw`, `googio/goclaw`, `wb253/goclaw`, `dzungtran/goclaw`, `ntheanh201/goclaw`, `YouCD/goclaw`, `otrumb/goclaw`, `danielabelski/goclaw`, `yatul/goclaw`...), nội dung README giống hệt nhau → đây là các clone/mirror của cùng một dự án gốc, có vẻ `nextlevelbuilder/goclaw` là bản active nhất (có PR #61 đang mở). Dự án tự mô tả là "OpenClaw rebuilt in Go" — không phải port trực tiếp từ `openclaw/openclaw`, mà kiến trúc lại độc lập bằng Go (binary tĩnh ~25MB, không cần Node.js). License: CC BY-NC 4.0 (phi thương mại) — cần lưu ý nếu tham khảo code, không copy nguyên trực tiếp nếu dùng cho SaaS thương mại.

Nguồn: https://github.com/nextlevelbuilder/goclaw , PR liên quan: https://github.com/nextlevelbuilder/goclaw/pull/61 ("feat: Add Claude CLI as LLM provider with MCP bridge").

### Pattern gọi Claude
goclaw hỗ trợ 20+ LLM provider, trong đó có 2 đường native khác nhau:
- **Anthropic API trực tiếp**: HTTP + SSE, có prompt caching — đây là path "chuẩn", dùng API key.
- **Claude CLI provider** (thêm ở PR #61): spawn subprocess `claude` binary, dùng chế độ headless `-p` (print/stream, non-interactive) với `--output-format stream-json` (và `--input-format stream-json` khi cần multi-turn), parse NDJSON streaming output.

File paths liên quan (PR #61):
- `internal/providers/claude_cli.go` — provider chính, logic spawn subprocess
- `internal/providers/claude_cli_auth.go` — kiểm tra/khởi tạo auth (`CheckClaudeAuthStatus()`, gọi `exec.Command(cliPath, "auth", "login")` nối stdin/stdout/stderr vào terminal cho OAuth flow trình duyệt)
- `internal/providers/claude_cli_hooks.go` — hook bảo mật (`WithClaudeCLISecurityHooks()`: shell deny patterns, path restriction)
- `internal/providers/claude_cli_mcp.go` — cấu hình MCP cho CLI (`WithClaudeCLIMCPConfig()`, `BuildCLIMCPConfig()`)
- `internal/providers/claude_cli_types.go` — types
- `internal/mcp/bridge_server.go` — MCP bridge server, expose tool nội bộ của goclaw ra Claude CLI qua streamable-http endpoint `/mcp/bridge`
- `cmd/onboard_claude_cli.go` — onboarding CLI

**Auth**: KHÔNG dùng `ANTHROPIC_API_KEY`. Dựa hoàn toàn vào session login sẵn có của CLI `claude` (subscription Pro/Max/Team/Enterprise qua OAuth cục bộ). Có detect subscription tier/email, tự trigger `claude auth login` khi chưa đăng nhập.

**Streaming**: parse stream-json NDJSON từ stdout của subprocess.

**Session/continuity**: truyền `providers.OptSessionKey` trong request options để giữ context hội thoại qua nhiều lần invoke (tương ứng `--resume`/session id của CLI).

**Concurrency**: mỗi request = một subprocess riêng, quản lý qua provider registry; không thấy giới hạn pool cụ thể trong tài liệu công khai — cần đọc source trực tiếp nếu áp dụng (tôi chỉ tiếp cận được qua GitHub UI fetch, không clone repo).

### Pattern để replicate bằng Go (tổng hợp, không chỉ riêng goclaw — pattern này lặp lại ở nhiều dự án Go khác như `claude-code-go`, `claude-agent-sdk-go`, `maruel/genai/providers/claudecode`)
1. `exec.CommandContext(ctx, "claude", "-p", "--output-format", "stream-json", "--verbose", ...)`.
2. Ghi prompt vào stdin (hoặc arg), đọc stdout theo dòng, decode JSON mỗi dòng (NDJSON) → emit sang channel Go để stream cho client (SSE/WebSocket).
3. Session tiếp diễn dùng `--resume <session_id>` hoặc `--continue`.
4. Auth dựa vào `claude` CLI đã login sẵn trên máy chạy (đọc credentials tại `~/.claude/` hoặc theo OS-specific config path) — **không** set `ANTHROPIC_API_KEY` nếu muốn dùng subscription.
5. Concurrency: giới hạn số subprocess đồng thời bằng semaphore (channel Go) vì mỗi subprocess launch model riêng, tốn RAM/CPU và có thể bị rate-limit theo tài khoản.

### ToS: dùng Claude subscription qua CLI trong backend tự động / SaaS

**Kết luận: KHÔNG được dùng cho SaaS đa người dùng.** OAuth token từ gói Free/Pro/Max/Team/Enterprise chỉ dành cho "ordinary use of Claude Code và các ứng dụng native của Anthropic" trên máy cá nhân. Dùng OAuth token đó trong sản phẩm/dịch vụ khác (kể cả qua Agent SDK) vi phạm Consumer ToS. Từ đầu 2026, Anthropic đã bắt đầu chặn phía server các client bên thứ ba xác thực bằng Claude subscription, trả lỗi "This credential is only authorized for use with Claude Code". Với sản phẩm/dịch vụ (kể cả nội bộ dùng cho automation lặp lại, chưa nói SaaS), Anthropic yêu cầu dùng **API key qua Claude Console hoặc cloud provider (Bedrock/Vertex)**.

→ Với use-case của user: giai đoạn local-only, cá nhân dùng `claude` CLI login bằng subscription cho việc dev/thử nghiệm cá nhân là chấp nhận được (ordinary use). Nhưng khi backend tự động hoá việc gọi Claude cho pipeline sinh story (kể cả chạy nội bộ nhiều lần/ngày, chưa nói multi-tenant SaaS) nên chuyển sang **Anthropic API key** ngay từ đầu để tránh rủi ro bị khóa tài khoản, và bắt buộc phải dùng API key khi lên SaaS multi-tenant.

Nguồn: Claude Code Docs "Legal and compliance" (code.claude.com), Anthropic Privacy Center "Terms of Service Updates", thảo luận GitHub liên quan (`akz142857/Halro` issue #351, `agentclientprotocol/claude-agent-acp` issue #337).

**Gemini CLI**: tương tự — dùng tài khoản Google cá nhân qua Gemini CLI thì áp dụng "Gemini Code Assist Privacy Notice for Individuals" (dữ liệu có thể dùng để train model), và truy cập trực tiếp dịch vụ đứng sau Gemini CLI bằng tool/service bên thứ ba bị coi là vi phạm ToS, có thể bị khóa tài khoản. Muốn dùng tự động hoá/backend phải dùng **Gemini API key qua Vertex AI / Google Cloud Platform Service Terms**.

**Khuyến nghị chung**: cả Claude lẫn Gemini — provider optional trong app nên implement qua API key (Anthropic API, Google AI/Vertex API), KHÔNG dựa vào CLI subscription cho bất kỳ luồng tự động nào, kể cả nội bộ. CLI subprocess chỉ hợp lý cho việc user tự chạy dev tool cá nhân (ví dụ Claude Code hỗ trợ code review nội bộ dự án), không phải cho pipeline sinh nội dung tự động của sản phẩm.

## 2. So sánh kiến trúc

| Phương án | Mô tả | Ưu điểm | Nhược điểm | Phù hợp? |
|---|---|---|---|---|
| (a) Go API + Postgres + Redis/NATS queue + Python GPU workers | Queue đơn giản (Asynq/NATS JetStream), Python worker tự quản lý state từng bước | Đơn giản, quen thuộc, latency thấp | Tự viết cơ chế retry/resume từng bước, dễ rối khi pipeline có 5-7 bước dài (LLM→image→TTS→align→render→upload) | Được nhưng tốn công tự xây durability |
| (b) Go API + workflow engine (Temporal/River/Asynq/Hatchet/Restate) | Engine quản lý durable execution, mỗi bước là 1 activity, tự động retry/resume theo bước, giữ state khi crash | Đúng nhu cầu "video 30min-3h, nhiều bước GPU dài, cần resume theo bước" — đây chính là bài toán chuẩn của durable workflow | Thêm thành phần hạ tầng, learning curve | **Khuyến nghị** |
| (c) Monolith modular (mọi thứ trong 1 Go binary, gọi Python qua subprocess/HTTP nội bộ) | Đơn giản nhất để chạy local | Không tách được GPU worker ra máy khác khi lên cloud sau này, khó scale SaaS | Không đáp ứng yêu cầu "sau này thành SaaS, chuyển GPU worker lên RunPod/Modal" | Không khuyến nghị cho roadmap SaaS |

### Đánh giá durable workflow engine cho Go (GPU job dài, resume theo bước)

- **Temporal**: chuẩn ngành cho durable execution, deterministic replay, rất mạnh cho pipeline nhiều bước phức tạp. Nhược: cần chạy cluster riêng (Temporal Service + DB Postgres/Cassandra + history/matching/frontend service) — over-kill cho 1 máy local Windows lúc đầu, nhưng scale SaaS rất tốt (Temporal Cloud có sẵn). Cộng đồng lớn, production-proven, ít rủi ro abandonment.
- **River**: Go-native, chỉ cần Postgres (không cần thêm service), insert job cùng transaction với app data, hỗ trợ batch, cron, scheduled, snooze. Nhẹ, đúng gu "Go + Postgres", rất hợp giai đoạn local-first vì không cần thêm hạ tầng ngoài Postgres đã có. Nhược: không có deterministic workflow replay như Temporal — logic multi-step/resume-per-step phải tự thiết kế bằng state machine (lưu job state ở DB, mỗi step là 1 job kế tiếp), tức "workflow" là compose thủ công từ nhiều job River, không phải first-class workflow engine.
- **Asynq**: tương tự River nhưng dùng Redis, phổ biến, đơn giản, có unique job/priority queue/concurrency limit per queue — tốt cho background job thường nhưng cũng không có workflow/step-resume model sẵn, cần tự implement state machine như River.
- **Hatchet**: định hướng task queue + durability, kiểm soát worker/retry/concurrency chi tiết hơn Temporal-style, đang phát triển nhanh trong 2026, nhưng ecosystem/độ trưởng thành thấp hơn Temporal.
- **Restate**: single binary, không cần external DB riêng (embedded log), latency thấp (single-digit ms) so với Temporal (50-200ms), có SDK Go, model rộng hơn (durable function/RPC/state/queue). Trẻ hơn Temporal về ecosystem nhưng vận hành đơn giản hơn nhiều — rất hợp máy 1 dev/local trước khi cần scale.

**Khuyến nghị cụ thể**: Bắt đầu với **River** (Postgres-based) cho giai đoạn local, vì: (1) đã cần Postgres cho app data nên không thêm hạ tầng, (2) đúng stack Go, (3) đủ dùng cho pipeline dạng step-chain nếu tự thiết kế state machine đơn giản (bảng `video_job_steps` với status/step_index/checkpoint_data, mỗi step hoàn thành enqueue step tiếp theo). Khi lên SaaS multi-tenant với nhiều pipeline phức tạp hơn (branching, human-in-loop approve, SLA retry phức tạp), cân nhắc migrate lên **Temporal** (hoặc Temporal Cloud) — migration từ River sang Temporal không quá tốn vì business logic của từng step (gọi LLM, ComfyUI, FFmpeg...) vẫn tái dùng được, chỉ thay lớp orchestration.

Trade-off risk: Restate hấp dẫn về vận hành nhưng ecosystem 2026 còn trẻ (rủi ro breaking change/community nhỏ hơn) — không khuyến nghị làm nền tảng chính cho SaaS lúc này, có thể theo dõi thêm.

## 3. Go↔Python worker contract, GPU scheduling, progress, storage, DB

- **Contract Go↔Python**: dùng **gRPC** (không phải REST thuần) cho lời gọi worker vì: type-safe qua protobuf, hỗ trợ streaming hai chiều (progress % trong lúc render/inference), overhead thấp hơn REST 40-60% theo benchmark thực tế. Queue message (River/NATS) dùng để **điều phối job** (job nào chạy tiếp theo), còn gRPC dùng cho **giao tiếp trực tiếp Go→Python worker** khi worker đã được assign job (ví dụ Go gọi gRPC `GenerateImage(job) stream Progress`). Lưu ý: gRPC Python có hạn chế khi fork process (ảnh hưởng PyTorch multiprocessing dataloader) — với ComfyUI thường gọi qua HTTP/WebSocket API sẵn có của ComfyUI thay vì tự viết gRPC wrapper, nên thực tế là **hybrid**: Go→ComfyUI qua HTTP+WebSocket (ComfyUI đã hỗ trợ sẵn), Go→Python worker tự viết (TTS, subtitle align) qua gRPC.
- **GPU single-slot scheduling**: vì chỉ 1 GPU (RTX 5060 Ti 16GB) nên cần semaphore cấp phát ở tầng Go (ví dụ 1 buffered channel dung lượng 1, hoặc queue có `max_concurrency=1` cho các job loại "gpu"). River hỗ trợ multi-queue với concurrency limit riêng từng queue — tạo queue riêng `gpu_jobs` với concurrency=1 là cách tự nhiên nhất, không cần code semaphore riêng.
- **Progress events**: **SSE** cho progress một chiều Go→browser (đơn giản hơn WebSocket, tự động reconnect qua EventSource, đủ dùng vì UI chỉ cần nhận % tiến độ/log, không cần gửi ngược nhiều). Dùng WebSocket chỉ nếu cần tương tác 2 chiều thực sự (ví dụ user can cancel/pause job qua cùng kênh) — có thể làm sau bằng 1 lệnh POST /cancel riêng, không bắt buộc phải là WebSocket.
- **Object storage**: **MinIO** (S3-compatible) chạy local Docker ngay từ đầu thay vì local FS thuần — lý do: code dùng S3 API (aws-sdk-go hoặc minio-go) từ ngày đầu, khi lên cloud (AWS S3/Cloudflare R2) chỉ đổi endpoint/credentials, không đổi code. Local FS thuần sẽ phải viết lại storage layer khi lên SaaS.
- **DB**: **Postgres + sqlc + pgx** — sqlc sinh Go code type-safe từ SQL thuần (không ORM, đúng gu KISS), pgx là driver Postgres hiệu năng cao chuẩn cho Go hiện đại. Migration dùng `golang-migrate` hoặc `goose` (dùng cái nào cũng được, chọn 1 và nhất quán — không cần cả hai).

## 4. Frontend

**Khuyến nghị: React + Vite + TanStack Router + TanStack Query**, không dùng Next.js, vì: app này là "tool-like" (dashboard quản lý job render video, theo dõi tiến độ real-time, form cấu hình pipeline) — không cần SEO/SSR mà Next.js mạnh về. TanStack Router có type-safety route/search-params tốt nhất hiện nay (2026), TanStack Query khớp tự nhiên với polling/SSE progress. Next.js vẫn là lựa chọn hợp lệ nếu sau này cần landing page public cho SaaS marketing — có thể tách landing page riêng (Next.js hoặc Astro) khỏi app dashboard (TanStack Start/Vite SPA) thay vì ép 1 framework làm cả hai việc.

## 5. Docker trên Windows với GPU

Setup: Windows 11 + WSL2 (Ubuntu backend) + Docker Desktop (dùng WSL2 engine, không dùng Hyper-V engine) + driver NVIDIA hỗ trợ CUDA-on-WSL (driver cài trên Windows, KHÔNG cài driver riêng trong WSL) + NVIDIA Container Toolkit cài trong distro WSL2. Verify bằng `docker run --gpus all nvidia/cuda:12.x-base nvidia-smi`. Compose layout: 1 file `docker-compose.yml` cho core (Go API, Postgres, MinIO, Redis nếu dùng Asynq — nhưng nếu chọn River thì bỏ Redis) + `docker-compose.gpu.yml` override riêng cho service Python/ComfyUI cần `deploy.resources.reservations.devices` với `driver: nvidia`. Dev experience: dùng `air` (Go hot-reload) cho service Go, mount code qua volume, KHÔNG rebuild image mỗi lần sửa code trong dev.

## 6. SaaS-readiness

- **Multi-tenancy**: dùng `tenant_id`/`org_id` là cột bắt buộc trên mọi bảng nghiệp vụ ngay từ đầu (kể cả bản local single-user) — row-level tenant scoping ở query layer (sqlc query luôn kèm `WHERE tenant_id = $1`). Không cần schema-per-tenant hay DB-per-tenant ở quy mô ban đầu — thêm phức tạp không cần thiết (YAGNI).
- **Auth**: JWT/session-based auth ngay từ đầu (kể cả single-user local, user vẫn login) để tránh phải retrofit auth middleware khi lên SaaS. OAuth cho YouTube Data API là auth riêng biệt (per-tenant, lưu refresh token mã hoá per tenant).
- **Quota**: bảng `tenant_quota` (số video/tháng, GPU-minutes, storage) check ở tầng enqueue job — thiết kế sẵn cột dù local-only chưa cần enforce.
- **GPU worker portable**: nhờ contract gRPC/HTTP rõ ràng giữa Go orchestrator và Python worker, việc chuyển worker từ máy local sang RunPod/Modal/cloud GPU chỉ là đổi network endpoint + auth token, không đổi protocol. Điều kiện: Python worker phải là service độc lập (đã đúng theo thiết kế containerized ở trên), không được nhúng logic gọi trực tiếp filesystem local (phải qua object storage S3-compatible đã chọn ở mục 3).

## Kiến trúc đề xuất — sơ đồ thành phần

```
                         ┌─────────────────────────┐
                         │   Frontend (React+Vite   │
                         │  TanStack Router/Query)  │
                         └───────────┬──────────────┘
                                     │ HTTP + SSE
                         ┌───────────▼──────────────┐
                         │        Go API             │
                         │  (net/http or Fiber/Chi)  │
                         │  - auth, tenant, quota    │
                         │  - job orchestration      │
                         │  - SSE progress broadcast │
                         └──┬──────────┬─────────┬───┘
                            │          │         │
                 ┌──────────▼───┐  ┌───▼────┐  ┌─▼────────────┐
                 │  Postgres    │  │ River  │  │  MinIO (S3)  │
                 │ (sqlc+pgx)   │  │ queue  │  │  object store│
                 │ tenant data  │  │(gpu=1) │  │  -> R2 later │
                 └──────────────┘  └───┬────┘  └──────────────┘
                                       │ gRPC / HTTP+WS
                     ┌─────────────────┼──────────────────────┐
                     │                 │                      │
             ┌───────▼──────┐  ┌───────▼───────┐    ┌─────────▼────────┐
             │ Python worker │  │ ComfyUI (HTTP │    │ FFmpeg render    │
             │ LLM/TTS/align │  │ + WS API)     │    │ worker (Go or    │
             │ (Ollama local │  │  image gen    │    │ Python subprocess│
             │  default,     │  │               │    │  wrapper)        │
             │  Claude/Gemini│  └───────────────┘    └──────────────────┘
             │  via API key  │
             │  optional)    │
             └───────────────┘
                     │
             ┌───────▼──────────────┐
             │ YouTube Data API      │
             │ (OAuth per-tenant)    │
             │ upload + analytics    │
             └────────────────────────┘

Toàn bộ chạy qua docker-compose (core) + docker-compose.gpu.yml (Python/ComfyUI
service dùng --gpus, GPU queue concurrency=1 chống tranh chấp VRAM).
```

## Cấu trúc thư mục đề xuất

```
repo/
├── docker-compose.yml
├── docker-compose.gpu.yml
├── go.mod
├── cmd/
│   ├── api/                # entrypoint Go API server
│   └── worker/             # entrypoint River worker process (Go side, orchestration steps)
├── internal/
│   ├── auth/                # JWT/session, tenant middleware
│   ├── tenant/               # tenant/quota model
│   ├── job/                  # job state machine, step definitions
│   ├── providers/
│   │   ├── llm/               # ollama, anthropic-api-key, gemini-api-key clients
│   │   ├── image/              # comfyui client (HTTP+WS)
│   │   ├── tts/                 # tts client (gRPC to python worker)
│   │   └── youtube/              # YouTube Data API OAuth client
│   ├── render/                # ffmpeg orchestration (long video assembly)
│   ├── storage/                # S3/MinIO client wrapper
│   ├── db/                     # sqlc generated code + queries/
│   └── sse/                    # progress broadcaster
├── db/
│   └── migrations/            # goose or golang-migrate files
├── proto/                      # gRPC .proto for go<->python contracts
├── workers-python/
│   ├── tts_service/             # gRPC server, TTS
│   ├── align_service/            # gRPC server, subtitle alignment
│   └── requirements.txt
├── comfyui/                     # ComfyUI docker + custom workflows json
├── web/                          # React + Vite + TanStack app
│   ├── src/routes/
│   ├── src/features/
│   └── vite.config.ts
└── docs/
    ├── system-architecture.md
    └── deployment-guide.md
```

## Giới hạn nghiên cứu / chưa xác minh
- Không clone/đọc trực tiếp source code goclaw (chỉ dùng GitHub UI fetch qua WebFetch — không có `gh` auth để search code chi tiết); mô tả file path/logic dựa trên nội dung PR #61 tóm tắt qua WebFetch, chưa đọc từng dòng code thật. Nếu cần độ chính xác cao hơn, nên `git clone` repo và đọc trực tiếp `internal/providers/claude_cli.go`.
- Không tìm được văn bản ToS chính thức gốc (chỉ qua bài phân tích bên thứ ba + docs Anthropic) — cần tự đọc lại `code.claude.com/docs/en/legal-and-compliance` và Anthropic Consumer Terms bản mới nhất trước khi ra quyết định pháp lý cuối cùng cho SaaS.
- Chưa benchmark thực tế Restate/Hatchet cho use-case cụ thể (video render 30min-3h) — khuyến nghị River/Temporal dựa trên đặc tính kiến trúc công bố, chưa có case study production tương tự app này.
- Chưa xét chi phí cụ thể RunPod/Modal cho giai đoạn SaaS (nằm ngoài phạm vi câu hỏi).

Status: DONE_WITH_CONCERNS

## Câu hỏi chưa giải quyết
1. Có cần đọc trực tiếp source `internal/providers/claude_cli.go` của goclaw (qua git clone) để lấy chính xác cơ chế pool subprocess/giới hạn concurrency, hay tóm tắt pattern chung là đủ?
2. Ngân sách/quyết định: dùng Anthropic API key trả phí ngay từ giai đoạn local-only, hay chấp nhận rủi ro dùng Claude Code CLI cá nhân tạm thời rồi migrate sau khi có doanh thu SaaS?
3. Muốn xác nhận: chọn River (không Redis) hay vẫn muốn có Redis cho mục đích khác (cache, rate-limit) khiến Asynq trở nên hợp lý hơn?
