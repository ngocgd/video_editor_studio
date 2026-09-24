# So sánh model sinh ảnh cho pipeline story-video xianxia/tiên hiệp (local 16GB vs API trả phí)

Ngày: 2026-09-24. Bối cảnh: video xianxia/tiên hiệp phong cách anime/donghua + bán tả thực, nhân vật lặp lại nhất quán qua hàng trăm scene, khung 16:9 1080p, kênh YouTube monetized. Phần cứng local: RTX 5060 Ti 16GB (Blackwell), 32GB RAM, ComfyUI.

## 1. Model local (open-weight)

| Model | Params | License | VRAM 16GB fit | Tốc độ/ảnh 1024-1080p (ước tính) | Ghi chú chất lượng |
|---|---|---|---|---|---|
| **Qwen-Image** (bản gốc, HF `Qwen/Qwen-Image`, 08/2025) | 20B | **Apache 2.0** (xác nhận trực tiếp từ model card) | GGUF Q4-Q6 ~12-13GB, FP8 ~16GB | ~20-30s | Text-in-image tốt nhất nhóm local, prompt adherence cao |
| **Qwen-Image-Edit-2509 / 2511** (bản edit mới nhất, kế thừa Qwen-Image gốc) | 20B | **Apache 2.0** (xác nhận trực tiếp từ model card HF) | Tương tự Qwen-Image | ~20-30s/edit | Multi-reference edit, điểm GEdit-Bench cao hơn Flux Kontext |
| Qwen-Image-2.1 (repo riêng `QwenLM/Qwen-Image-2.1`) | 7B | **Qwen Research License — non-commercial**, cần mua license riêng cho thương mại (contact model-business@notice.qwencloud.com) | — | — | **Tránh cho kênh monetized** — xác nhận trực tiếp từ file LICENSE trên GitHub, đúng như báo cáo trước đã cảnh báo |
| **Z-Image Turbo** (Tongyi/Alibaba, 11/2025, 6B) | 6B | **Apache 2.0** | FP8 ~8GB, BF16 ~14-16GB, GGUF ~6GB | ~2-3s (8 bước, RTX 4090; 5060 Ti sẽ chậm hơn nhưng vẫn hàng giây) | Nhanh nhất nhóm local, ảnh thực/cinematic tốt, bilingual text render; ít dữ liệu benchmark riêng cho anime/donghua |
| FLUX.2 [klein]-4B | 4B | **Apache 2.0** | Fit thoải mái 16GB | Nhanh (mô hình "sub-second" trên datacenter GPU) | Model mới nhất BFL cho local, chất lượng thấp hơn Flux.2 dev nhưng license sạch |
| FLUX.2 [klein]-9B | 9B | FLUX Non-Commercial License | Fit 16GB với FP8 | Nhanh | Cần mua license thương mại để tự host cho kênh kiếm tiền |
| FLUX.2 [dev] (32B) | 32B | Non-Commercial; self-host thương mại = $999/tháng (100k ảnh/tháng) từ BFL | Không fit 16GB ở full, cần quant nặng | Chậm | Không khả thi cho 16GB + license đắt |
| FLUX.1 schnell | 12B | **Apache 2.0** | FP8 ~12GB | ~10-15s (ít bước) | Nhanh, chất lượng thấp hơn Qwen-Image, dùng làm fallback tốc độ |
| FLUX.1 dev / Kontext dev | 12B | Non-Commercial (output qua API BFL mới được thương mại) | ~13GB | ~20s | Vẫn rủi ro license như báo cáo trước đã nêu |
| HunyuanImage 3.0 (Tencent, 84B MoE, ~14B active) | 84B (MoE) | Tencent Hunyuan Community License (không phải Apache) | **Không fit 16GB** — khuyến nghị ≥3×80GB, tối thiểu ~24GB với offload nặng + 192GB RAM | Rất chậm nếu offload | **Loại khỏi lựa chọn cho RTX 5060 Ti 16GB** |
| HiDream-I1 | Full/Dev/Fast | **MIT** | Full >27GB (không fit); bản NF4 4-bit <16GB; FP8 >16GB | Vừa | Có thể chạy bản NF4 quantize nhưng mất chất lượng đáng kể so với Full; ít phổ biến hơn Qwen-Image/Z-Image trong 2026 |
| SDXL + Illustrious-XL / NoobAI-XL (anime finetune) | 2.6B (SDXL base) | Base SDXL: **OpenRAIL++** (cho phép thương mại có ràng buộc sử dụng có trách nhiệm); nhiều checkpoint Illustrious/NoobAI trên Civitai dùng license riêng — **phải kiểm tra từng checkpoint** | Fit dễ dàng 16GB, thậm chí 8GB | Nhanh (~5-10s, ít step hơn diffusion transformer lớn) | **Chất lượng anime/donghua tốt nhất nhóm local** — hệ sinh thái 198K+ LoRA trên Civitai, quy trình train character LoRA đã chuẩn hóa (55-60 ảnh, Dim 64/Alpha 32); nhược điểm: cần ghép nối nhiều LoRA/embedding, ít mạnh về prompt adherence phức tạp và text-in-image so với Qwen-Image |

**Nguồn:** model card HF (Qwen-Image, Qwen-Image-Edit-2509), GitHub LICENSE file (QwenLM/Qwen-Image-2.1), BFL licensing page (bfl.ai/licensing), HF Tongyi-MAI/Z-Image-Turbo, HF HiDream-ai, Civitai Illustrious ecosystem page, GitHub black-forest-labs/flux2.

## 2. Model API trả phí

Bảng xếp hạng Artificial Analysis Image Arena (text-to-image, Elo, truy xuất trực tiếp 24/09/2026):

| Rank | Model | Elo | Ngày phát hành |
|---|---|---|---|
| 1 | GPT Image 2.5 Sunburst (max) | 1196 | 09/2026 |
| 2 | GPT Image 2.5 Flare (max) | 1190 | 09/2026 |
| 3 | GPT Image 2 (high) | 1171 | 04/2026 |
| 4 | Grok Imagine Image 2.0 | 1154 | 08/2026 |
| 6 | Nano Banana 2 (Gemini 3.1 Flash Image) | 1122 | 02/2026 |
| 10 | Nano Banana Pro (Gemini 3 Pro Image) | 1101 | 11/2025 |
| 14 | Qwen-Image-3.0-Pro (API, khác bản local) | 1088 | 07/2026 |
| 15 | Seedream 5.0 Pro | 1078 | 07/2026 |
| — | Ideogram 4.0 Quality — **model open-source xếp cao nhất, Elo ~1010, thấp hơn hẳn top-15 proprietary** | ~1010 | — |

Toàn bộ top-15 hiện là API độc quyền, không có model local nào lọt top-15 arena (Ideogram 4.0 là model mở gần nhất nhưng đã tụt lại đáng kể). Điều này khớp với xu hướng: API trả phí dẫn đầu tuyệt đối về chất lượng thô, model local thắng về chi phí/license/kiểm soát.

| Model | Giá/ảnh | Character consistency / multi-ref | Text-in-image | Content policy bạo lực giả tưởng | Ghi chú |
|---|---|---|---|---|---|
| **Nano Banana Pro** (Gemini 3 Pro Image, Google) | $0.134/ảnh (1K/2K), $0.24/ảnh (4K); batch/flex giảm nửa giá ($0.067/2K) | Mạnh — multi-reference editing, giữ identity tốt qua nhiều lần edit | Tốt nhất nhóm Google, SynthID watermark | Cấm bạo lực "sensational/gratuitous" (thật hoặc hư cấu), nhưng cho phép theo ngữ cảnh nghệ thuật/giáo dục — bạo lực giả tưởng mức vừa phải (chiến đấu tiên hiệp, kiếm hiệp) thường qua được, cảnh máu me quá mức có thể bị chặn | Elo rank #10 nhưng vẫn rất mạnh về consistency |
| Nano Banana 2 (Gemini 3.1 Flash Image) | Thấp hơn Pro (chưa xác nhận giá chính xác, dòng Flash rẻ hơn Pro) | Tốt, nhanh hơn Pro | Tốt | Cùng chính sách Gemini | Elo #6, nhanh/rẻ hơn cho scene phụ |
| Imagen 4 (Google, qua Vertex AI) | Chưa verify giá 2026 cụ thể trong phiên này | Ít mạnh về multi-ref hơn Nano Banana Pro (Imagen tối ưu photorealism hơn edit) | Khá | Cùng chính sách Google | **Chưa verify đầy đủ — xem câu hỏi tồn đọng** |
| **gpt-image-1** | Low $0.011 / Medium $0.042 / High $0.167 (1024x1024) | Có edit/inpainting nhưng multi-ref yếu hơn Nano Banana Pro | Tốt | Chính sách OpenAI cấm bạo lực đồ họa cực đoan, nội dung giả tưởng vừa phải thường được duyệt | **Bị retire 23/10/2026** — không nên build pipeline mới trên gpt-image-1, dùng GPT Image 1.5/2 hoặc Mini thay thế |
| GPT Image 2 (high) | Chưa verify giá cụ thể phiên này (API mới 04/2026) | Chưa verify | Elo #3 arena — rất mạnh | Chính sách OpenAI | Model mạnh nhất còn "sống" lâu dài trong dòng GPT Image, cần verify giá trước khi build |
| **Grok Imagine Image 2.0** (xAI) | $0.04/ảnh (1K) - $0.08/ảnh (2K) output, $0.01/ảnh input | Compose từ tối đa 5 ảnh tham chiếu/lần gọi, region-level edit | Trung bình | **Đã siết chặt filter sau vụ deepfake 01/2026**, bỏ free tier 19/03/2026, chỉ dùng được cho subscriber trả phí; chính sách bạo lực chưa rõ ràng bằng Google/OpenAI | Elo #4 — rất cạnh tranh, nhưng rủi ro chính sách/thương hiệu (xAI từng bị chỉ trích về nội dung) cần cân nhắc cho kênh monetized |
| **Seedream 4.0/4.5** (ByteDance) | 4.0: $0.018-0.02/ảnh; 4.5: $0.03-0.04/ảnh (67% đắt hơn, thêm 4K + text tốt hơn) | Multi-reference mạnh — 10 ảnh ref, batch 15 output/lần | Cải thiện ở 4.5 | Chưa verify chi tiết chính sách bạo lực trong phiên này | **Rẻ nhất nhóm API chất lượng cao** — ứng viên tốt cho character sheet/thumbnail giá rẻ |
| Midjourney | N/A — **không có API chính thức** (đã xác nhận qua nhiều nguồn 2026, mọi "Midjourney API" trên thị trường là wrapper không chính thức, rủi ro khóa tài khoản) | Mạnh về mặt thẩm mỹ qua giao diện | N/A | N/A | **Loại khỏi pipeline tự động hóa** do không có API chính thức — chỉ dùng thủ công nếu cần |

**Nguồn:** artificialanalysis.ai/image/leaderboard/text-to-image (truy xuất trực tiếp), pricepertoken.com (Nano Banana Pro, GPT Image), docs.x.ai/developers/pricing, openrouter.ai/bytedance-seed, docs.cloud.google.com (Gemini responsible AI policy), support.google.com/gemini prohibited-use policy, nhiều nguồn xác nhận độc lập về việc Midjourney chưa có API chính thức tính đến 09/2026.

## 3. Trả lời: Qwen-Image có phải lựa chọn local tốt nhất?

**Không hoàn toàn — tùy tiêu chí, nên phối hợp 2-3 model local thay vì 1 model duy nhất:**

- **Text-in-image (thumbnail) + prompt adherence phức tạp (scene có nhiều yếu tố/nhân vật)**: Qwen-Image (Apache 2.0) vẫn là lựa chọn tốt nhất nhóm local — không đối thủ nào trong nhóm Apache 2.0 vượt được ở khoản render chữ chính xác.
- **Tốc độ sinh ảnh hàng loạt (150-300 ảnh/video)**: Z-Image Turbo (Apache 2.0, 6B, ~2-3s/ảnh) nhanh hơn Qwen-Image 20B đáng kể (~20-30s/ảnh) — với khối lượng lớn, chênh lệch tốc độ này quyết định pipeline có chạy nổi trong ngân sách thời gian hay không.
- **Chất lượng anime/donghua thuần túy**: SDXL + Illustrious-XL/NoobAI-XL vẫn vượt trội các model diffusion-transformer lớn ở phong cách anime/webtoon cụ thể, nhờ hệ sinh thái LoRA khổng lồ chuyên biệt cho tiên hiệp/xianxia trên Civitai — nhưng cần kiểm tra license từng checkpoint (base SDXL là OpenRAIL++, checkpoint cộng đồng có thể khác).
- **Character consistency/edit giữa scene**: Qwen-Image-Edit-2509/2511 (Apache 2.0, kế thừa Qwen-Image gốc) vẫn là lựa chọn tốt nhất, điểm GEdit-Bench cao hơn Flux Kontext.

**Kết luận: Qwen-Image là "trục chính" tốt nhất cho text-render + edit/consistency (giữ nguyên khuyến nghị báo cáo trước), nhưng Z-Image Turbo nên là engine mặc định cho khối lượng lớn scene phụ vì tốc độ, và Illustrious-XL/NoobAI-XL nên bổ sung khi cần phong cách anime/donghua thuần túy hơn Qwen-Image (Qwen-Image thiên về ảnh bán thực/illustration hơn anime 2D thuần).** Không có model local Apache 2.0/MIT nào lọt top-15 arena — chấp nhận khoảng cách chất lượng với API trả phí là đánh đổi bắt buộc khi ưu tiên chi phí $0 + license an toàn.

## 4. Chiến lược hybrid đề xuất

1. **Local mặc định cho khối lượng lớn (150-300 ảnh scene/video)**: Z-Image Turbo (tốc độ) cho scene nền/phụ, Qwen-Image cho scene chính cần chữ/prompt phức tạp, Illustrious-XL/NoobAI-XL + LoRA nhân vật cho scene cần phong cách anime/donghua rõ nét. Chi phí biên = $0 (chỉ điện + thời gian máy).
2. **API trả phí cho việc có đòn bẩy cao, số lượng thấp:**
   - **Character sheet gốc** (tạo reference ảnh nhân vật chuẩn để train LoRA hoặc làm ảnh ref cho PuLID/Qwen-Image-Edit): dùng Nano Banana Pro hoặc Seedream 4.5 — multi-reference consistency mạnh nhất thị trường, chỉ cần vài chục ảnh/nhân vật nên chi phí không đáng kể.
   - **5 thumbnail/video**: dùng Nano Banana Pro hoặc GPT Image 2 — text-in-image + độ bắt mắt cao hơn hẳn model local, ảnh hưởng trực tiếp CTR nên đáng chi trả phí dù rẻ hơn không quan trọng bằng chất lượng.
3. **Ước tính chi phí cho 1 video ~1 giờ (150-300 ảnh scene + 5 thumbnail):**
   - Scene: 100% local → **$0** (đã tính trong chi phí điện/thời gian máy ở báo cáo trước, ~30-60 phút compute).
   - Thumbnail (5 ảnh) qua Nano Banana Pro (2K, $0.134/ảnh): **~$0.67/video**.
   - Nếu dùng Seedream 4.5 thay thế (rẻ hơn, $0.035/ảnh trung bình): **~$0.18/video**.
   - Character sheet là chi phí one-time/nhân vật (không lặp mỗi video), ước ~10-20 ảnh/nhân vật × $0.134 = **$1.3-2.7/nhân vật**, chia đều cho hàng chục video tái sử dụng nhân vật đó → không đáng kể trên đầu video.
   - **Tổng chi phí biến đổi/video: dưới $1** (chỉ phần thumbnail qua API), phù hợp ngân sách kênh nhỏ/vừa.

## 5. Hạn chế nghiên cứu

Chưa benchmark thực nghiệm tốc độ Z-Image Turbo/Qwen-Image trên chính RTX 5060 Ti (số liệu suy từ RTX 4090, cần giảm ước tính ~20-30%). Chưa verify giá API Imagen 4 và GPT Image 2 cụ thể trong phiên nghiên cứu này (thời gian có hạn, nguồn tìm được không cho số giá rõ ràng). Chưa test thực tế mức độ Gemini/Grok/OpenAI chặn nội dung bạo lực tiên hiệp cụ thể (cảnh chặt chém, tu luyện đổ máu) — chính sách viết chung chung "context matters", cần thử nghiệm prompt thực tế trước khi cam kết pipeline production. License từng checkpoint Illustrious-XL/NoobAI-XL cụ thể trên Civitai chưa được kiểm tra từng cái — cần xác minh license riêng của checkpoint dự định dùng trước khi thương mại hóa.

Status: DONE_WITH_CONCERNS

## Câu hỏi chưa giải quyết

1. Giá API chính xác của Imagen 4 (Vertex AI) và GPT Image 2 (không phải bản Sunburst/Flare) chưa xác minh được trong phiên này — cần tra cứu trực tiếp Google Cloud/OpenAI pricing page trước khi lập ngân sách chính thức.
2. Cần test thực nghiệm prompt bạo lực tiên hiệp cụ thể (chiến đấu, tu luyện, máu) trên Gemini/GPT Image/Grok Imagine để biết ngưỡng chặn thực tế, vì chính sách công khai chỉ nói chung chung.
3. Cần benchmark tốc độ thực tế Qwen-Image/Z-Image Turbo/Illustrious-XL trên chính RTX 5060 Ti (không phải suy từ RTX 4090) để tính lại ngân sách thời gian pipeline chính xác.
4. Cần kiểm tra license cụ thể của checkpoint Illustrious-XL/NoobAI-XL định dùng (ví dụ WAI, Nova, Prefect...) trên Civitai — một số checkpoint cộng đồng có điều khoản riêng ngoài OpenRAIL++ gốc.
5. Chưa xác minh liệu Seedream 4.x có giới hạn khu vực truy cập API (ByteDance, có thể cần entity Trung Quốc hoặc hạn chế cho một số quốc gia) — cần kiểm tra trước khi tích hợp vào pipeline tự động hóa.
