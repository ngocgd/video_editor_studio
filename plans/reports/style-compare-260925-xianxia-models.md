# So sánh phong cách xianxia: Z-Image Turbo vs Illustrious-XL (+ smoke Qwen-Image-Edit-2511)

Ngày chạy: 2026-09-25 (Asia/Bangkok). Máy: RTX 5060 Ti 16GB (desktop chiếm ~1.08 GiB trong lúc chạy). Image `loomtale/comfyui-spike:local` (ComfyUI 78368ea, ComfyUI-GGUF 6ea2651). Container chạy qua `docker compose -f deploy/compose.yml -f deploy/compose.gpu.yml up -d comfyui`, dùng network internal, không publish port, volume `loomtale_models` mount `:ro`; mọi lệnh gọi đi qua `docker exec … curl`. Chạy xong đã stop và rm container.

## Thiết lập
- Tất cả ảnh 1024x1024, seed cố định: A=1001, B=1002, C=1003.
- **Z-Image Turbo int8** (`comfyui/workflows/z-image-turbo-int8-smoke.json`, giữ nguyên graph, chỉ thay prompt và seed): 8 step, cfg 1, res_multistep, shift 3. Biến thể "donghua" thêm hậu tố `, digital painting, donghua illustration style`.
- **Illustrious-XL v1.1 + LoRA "Xianxia Art Style"** ở strength 0.8 (`comfyui/workflows/illustrious-xl-txt2img.json`): euler_ancestral/normal, 28 step, cfg 6. Positive dùng tag kiểu booru, có `masterpiece, best quality, very aesthetic, absurdres, xianxia style, …`. Negative là bộ chuẩn trên model card (`worst quality, bad anatomy, bad hands, extra digits, …, nsfw`).
- **Giây** = thời gian thực từ lúc submit đến lúc có history. Ảnh đầu tiên của mỗi engine gồm cả thời gian nạp model.
- **VRAM** = `vram_total − min(vram_free)` của cả thiết bị (đã gồm ~1.08 GiB của desktop), lấy mẫu mỗi 1s qua `/system_stats`.
- Ảnh lưu tại `…/scratchpad/style-compare/`.

## Kết quả

| Prompt | Engine | File | Giây | VRAM đỉnh (GiB, cả card) | Nhận xét |
|---|---|---|---|---|---|
| A (kiếm tiên trên biển mây) | Z-Image photo | A-zimage-photo.png | 42.2 (cold) | 12.70 | Đúng ý nhất: đứng trên phi kiếm, núi ngọc, biển mây. Hanfu trắng đơn giản, trông như ảnh cosplay/phim truyền hình. Tay và chân ổn. |
| A | Z-Image donghua | A-zimage-donghua.png | 19.7 | 13.61 | Tranh phẳng kiểu hoạt hình Trung Quốc, bố cục đúng, chân đặt đúng trên kiếm. Nét hơi "trẻ con", thiếu cảm giác hoành tráng. |
| A | Illustrious+LoRA | A-illustrious.png | 21.1 (cold) | 12.35* | Trang phục đẹp nhất (viền thêu, dải lụa, tua đỏ), đúng chất donghua tiên hiệp. **Sai ý chính**: nhân vật cầm kiếm, không đứng trên kiếm. Mắt nhắm/mặt hơi lạ. |
| B (tông môn trên đỉnh núi) | Z-Image photo | B-zimage-photo.png | 19.9 | 13.15 | Ảnh thật, kiến trúc Trung Hoa thuyết phục, có đủ hạc/đèn/thông/bậc đá. Trời xám nhạt, ít "tiên khí". Biển chữ là chữ giả. |
| B | Z-Image donghua | B-zimage-donghua.png | 19.6 | 12.88 | **Đẹp nhất hàng B**: phong cách quốc phong/thủy mặc, sương mù, hạc, đèn lồng, thông, rất hợp xianxia. Biển chữ là chữ giả. |
| B | Illustrious+LoRA | B-illustrious.png | 12.8 | 7.76 | Tranh concept kiểu game: tháp nhiều tầng, đảo núi lơ lửng, ánh hoàng hôn. Chất "fantasy" mạnh. Hạc chỉ là chấm chim nhỏ, kiến trúc hơi lai Nhật và kém chi tiết. |
| C (song đấu trên không, kiếm khí xanh/đỏ) | Z-Image photo | C-zimage-photo.png | 20.1 | 13.50 | Không ra ảnh thật mà ra 3D hoạt hình. Hai nhân vật bay, kiếm khí xanh/đỏ va chạm rất rõ. Trang phục giống ninja/võ phục hơn hanfu. |
| C | Z-Image donghua | C-zimage-donghua.png | 19.8 | 13.17 | Ra anime shonen kiểu Nhật (giống Dragon Ball), không phải donghua. Trang phục là võ phục, tóc dựng. Hành động rõ, tay ổn. |
| C | Illustrious+LoRA | C-illustrious.png | 12.8 | 7.77 | Cảm giác xianxia mạnh nhất (tóc dài, hanfu, dải khí, trời sao). Nhưng nhân vật không ở giữa không trung, kiếm xanh ngắn và cầm sai, kiếm khí đỏ thành vệt lửa. Hai mặt nam giống nhau. |

\* Illustrious A: 12.35 GiB là do phần dư của Z-Image chưa bị evict. Khi đã ổn định (B, C) mức dùng là 7.76–7.77 GiB cả card, tức **~6.7 GiB cho riêng Illustrious**. Illustrious warm **12.8 s/ảnh** (28 step), cold 21.1s.

Z-Image warm ~19.7–20.1 s/ảnh, cold 42s. VRAM ~12.7–13.6 GiB là vì ComfyUI giữ text encoder và model trong VRAM, không phải model thực sự cần ngần đó. RAM container cao nhất đo được là 7.83 GiB (sau lượt Illustrious), dưới giới hạn `mem_limit` 10g.

### Smoke Qwen-Image-Edit-2511 (Q4_K_M, unsloth)
- Workflow `comfyui/workflows/qwen-image-edit-2511-smoke.json`: `TextEncodeQwenImageEditPlus` → `FluxKontextMultiReferenceLatentMethod(index_timestep_zero)`, 20 step, cfg 4.
- Ảnh gốc: A-zimage-donghua.png. Lệnh sửa: "cầm hồ lô ngọc xanh phát sáng tay trái, giữ nguyên mặt, trang phục, tư thế, kiếm và nền".
- Kết quả `qwen-edit-2511-smoke.png`: **success, 264s cold** (gồm nạp TE 7B và model), VRAM đỉnh **14.28 GiB** (còn trống ~1.6 GiB).
- Chất lượng edit tốt: thêm đúng hồ lô, giữ nguyên mặt, dáng, kiếm và nền. Lỗi nhỏ: dây lưng đổi từ xanh navy sang xanh ngọc, có quầng xanh lá trên tay áo/tóc.
- Log: `loaded completely … full load: True`, không có lowvram/CPU offload. Thời gian tương đương 2509 (244s cold), vẫn vượt ngân sách 90s.

## Đánh giá ngắn
- **Không engine nào thắng cả 3 prompt.** Z-Image bám prompt tốt nhất: tư thế ngự kiếm, hai người bay giữa không trung. Illustrious+LoRA cho trang phục và "vibe" xianxia đẹp nhất nhưng bỏ qua ý hành động/không gian.
- Hậu tố "donghua" của Z-Image cho kết quả không ổn định: B ra quốc phong rất đẹp, C lại ra anime Nhật. Muốn có phong cách nhất quán cho kênh thì cần hậu tố cụ thể hơn (vd. "Chinese 3D donghua, xianxia, guofeng"), hoặc LoRA cho Z-Image.
- Illustrious nhanh gấp ~1.5 lần và dùng VRAM chỉ bằng một nửa Z-Image. Hợp làm engine chính cho shot nhân vật tĩnh (chân dung, trang phục). Với shot hành động phức tạp nên dùng Z-Image, hoặc Illustrious kèm ControlNet pose (chưa test).
- Qwen-2511 edit giữ nhân vật rất tốt. Hợp với vai trò character sheet / sửa ảnh ref như kế hoạch, không dùng cho từng scene.
- Mỗi cấu hình chỉ chạy 1 seed, nên kết luận mang tính định hướng, không phải thống kê.

## License (đã kiểm tra trực tiếp)
- Z-Image Turbo: Apache-2.0.
- Qwen-Image-Edit-2511 (base + unsloth GGUF): Apache-2.0.
- Illustrious-XL v1.1: SDXL licence (CreativeML Open RAIL++-M) theo `license_link` của repo. "Licensor claims no rights in the Output", **được dùng output cho YouTube có kiếm tiền**, chỉ phải tuân thủ các hạn chế sử dụng ở Attachment A. Bản v0.1 (FAIPL-1.0-SD, card ghi "discourages … monetization") nên không dùng.
- LoRA "Xianxia Art Style" (civitai.com/models/1955005, version 2212667): Civitai cho phép commercial `Image`/`Rent`/`Sell`/`SellMerge`, không cần credit. Đây chỉ là cờ quyền do creator tự khai trên Civitai, không phải văn bản license đầy đủ.
- Chi tiết sha256/revision xem `plans/reports/spike-260924-model-downloads.md` mục Batch 2.

## Đã xóa và dung lượng đĩa
- Đã xóa: `/models/diffusion_models/Qwen-Image-Edit-2509-Q4_K_M.gguf` (13.07 GB), xóa sau khi smoke 2511 đạt. Cũng đã gỡ `comfyui/workflows/qwen-image-edit-2509-smoke.json`, và `scripts/comfyui-smoke-spike.sh` giờ trỏ sang workflow 2511.
- Tải mới 20.41 GB (≤25 GB). Volume hiện tại 45.41 GB.
- C: trống trước 232,470,863,872 B, sau 215,498,772,480 B (−16.97 GB). Vhdx của WSL2 chưa trả lại 13 GB vừa xóa cho Windows, cần compact nếu muốn thu hồi.

## Câu hỏi chưa giải quyết
1. Hàng A/C của Illustrious bỏ qua ý "đứng trên kiếm / giữa không trung". Có nên thử ControlNet (OpenPose/depth) cho SDXL, hoặc prompt/LoRA "sword riding" riêng không?
2. Cần chốt 1 "style lock" cho cả kênh: Z-Image kèm hậu tố quốc phong cụ thể, hay Illustrious+LoRA? Nên thử 3–5 seed mỗi prompt trước khi quyết.
3. Quyền của LoRA chỉ dựa vào cờ creator khai trên Civitai. Nếu cần chắc chắn về pháp lý, nên lưu snapshot trang model làm bằng chứng.
4. Qwen-2511 vẫn mất ~264s/edit và chỉ còn ~1.6 GiB VRAM trống khi desktop tải nhẹ. Rủi ro OOM khi desktop ở mức ~5.4 GB vẫn chưa được đo lại.
5. Có nên compact vhdx (Docker Desktop → "Clean / Purge data" hoặc `Optimize-VHD`) để lấy lại ~13 GB trên C: không? Việc này chưa làm vì ảnh hưởng tới Docker Desktop toàn máy.
