# Forum/cộng đồng sentiment 2026 — 4 model ảnh local cho xianxia/wuxia/fantasy

Đã đọc 2 báo cáo trước (license/adoption/benchmark), báo cáo này chỉ tập trung **cảm nhận người dùng thực tế** (Reddit, Civitai, HF discussions, GitHub) cho illustration xianxia/wuxia/fantasy + character consistency.

## Giới hạn thu thập (đọc trước khi dùng report)

- Công cụ WebFetch **không truy cập được reddit.com/old.reddit.com trực tiếp** trong phiên này (bị chặn ở tầng hạ tầng) — mọi nội dung Reddit dưới đây đến từ WebSearch snippet/third-party tổng hợp (blog trích dẫn reddit), **không phải đọc trực tiếp thread**, nên không có link thread + quote gốc kiểm chứng được cho phần lớn claim Reddit.
- Không tìm được nội dung Bilibili/Zhihu/LiblibAI tiếng Trung liên quan trong phiên này — phần xianxia costume dựa trên Civitai/HF (cộng đồng chủ yếu tiếng Anh) + 1 LoRA tiếng Trung tìm được qua Civitai search.
- Civitai review text (nội dung comment cụ thể) không trích xuất được qua WebFetch (trang render JS phía client) — chỉ lấy được **số liệu rating tổng hợp** (số lượt, %sao), không phải quote cá nhân.
- Mọi claim dưới đây gắn nguồn cụ thể; đoạn nào không xác minh được ghi **[UNVERIFIED]**. Không có quote nào bị bịa — chỗ nào không tìm được quote gốc thì paraphrase có ghi nguồn thay vì trích dẫn giả.

## 1. Z-Image Turbo (Tongyi/Alibaba 6B, Apache 2.0)

**Sentiment: đa số tích cực, ít complaint nghiêm trọng** (ước lượng ~80% positive / 15% mixed / 5% negative, dựa trên tỷ lệ rating Civitai).

- Civitai: nhiều checkpoint/workflow dựa trên Z-Image Turbo đạt **5 sao với 100-328 lượt review** (vd. "CyberRealistic Z-Image Turbo" 328 reviews, "Zimage Turbo by Stable Yogi" 209 reviews, tất cả 5 sao) — [civitai.com/models/2218365](https://civitai.com/models/2218365/reviews?modelVersionId=2547120), [civitai.com/models/2221503](https://civitai.com/models/2221503/reviews?modelVersionId=2547276). Lưu ý: rating Civitai thiên vị tích cực có hệ thống (chỉ người thích mới review), không phải sample ngẫu nhiên.
- Điểm khen lặp lại nhiều nguồn: hết "Flux chin"/"plastic skin" điển hình, da và tóc tự nhiên hơn, tốc độ vượt trội (1-8 step, ~2-8s/ảnh so với 20-30s của Qwen-Image) — [diffusiondoodles.substack.com](https://diffusiondoodles.substack.com/p/z-image-turbo-fast-and-functional).
- Complaint kỹ thuật xác nhận từ chính HF discussion của Tongyi-MAI: model vẫn lỗi giải phẫu ở pose phức tạp (đặc điểm chung của few-step distilled model); team phải đánh đổi "diversity recovery" giảm từ 70%→58% để đổi lấy anatomical stability — [huggingface.co/Tongyi-MAI/Z-Image-Turbo/discussions/135](https://huggingface.co/Tongyi-MAI/Z-Image-Turbo/discussions/135).
- Text rendering: yếu hơn Qwen-Image (2512) khi cần spell chính xác chuỗi ký tự dài, nhưng đủ cho text ngắn/biển hiệu — so sánh benchmark 5-prompt trên dev.to/Medium — [dev.to/gary_yan...](https://dev.to/gary_yan_86eb77d35e0070f5/qwen-image-2512-vs-z-image-turbo-5-prompt-benchmark-which-model-is-better-5ni).
- Prompt adherence: theo đúng chỉ dẫn nhưng "làm mượt" thẩm mỹ hơn (aesthetic smoothing) so với Qwen-Image bám sát literal hơn — cùng nguồn benchmark trên.
- GitHub issue của Pollinations ghi nhận chất lượng ảnh thấp hơn kỳ vọng trên platform của họ (khả năng do cấu hình JPEG 95% nén ảnh, không phải lỗi model gốc) — [github.com/pollinations/pollinations/issues/6489](https://github.com/pollinations/pollinations/issues/6489).
- Một quote gián tiếp qua blog (chưa verify trực tiếp thread gốc, ghi nhận **[UNVERIFIED — nguồn thứ cấp]**): user Reddit "Regular-Forever5876" thử prompt gore và nhận xét model "hiểu gore rất tốt" — cho thấy Z-Image Turbo **không có safety filter mạnh** như Qwen-Image, phù hợp cảnh chiến đấu tiên hiệp có máu.
- Xianxia/costume/flying sword: không tìm được thảo luận cộng đồng riêng cho chủ đề này — Z-Image Turbo là base model tổng quát (photoreal-leaning), không có LoRA ecosystem lớn cho phong cách donghua/anime tiên hiệp như Illustrious. **[UNVERIFIED cho riêng khía cạnh xianxia]**.
- Pairing: cộng đồng dùng chủ yếu với LoRA phong cách thực tế (CyberRealistic, UltraReal workflow) — chưa thấy LoRA xianxia/wuxia riêng cho Z-Image Turbo.

## 2. Qwen-Image-Edit-2509 vs 2511 (Apache 2.0)

**Sentiment: tích cực nghiêng về 2511 là upgrade thực chất**, nhưng nguồn chủ yếu là blog/HF chứ không phải Reddit trực tiếp trong phiên này — độ tin cậy trung bình.

- Cải tiến xác nhận từ HF model card + blog ComfyUI chính thức: giảm "image drift", character consistency tốt hơn rõ rệt (đặc biệt group-shot nhiều người), tích hợp sẵn LoRA phổ biến, cải thiện geometric reasoning — [huggingface.co/Qwen/Qwen-Image-Edit-2511](https://huggingface.co/Qwen/Qwen-Image-Edit-2511), [blog.comfy.org/p/qwen-image-edit-2511...](https://blog.comfy.org/p/qwen-image-edit-2511-and-qwen-image).
- Deep-dive bên thứ ba (MyAIForce) kết luận 2511 đáng nâng cấp nếu quan tâm consistency/identity retention, có so sánh model size/settings cụ thể — [myaiforce.com/qie-2511](https://myaiforce.com/qie-2511/).
- Censorship/softening bạo lực: **không tìm được** complaint cụ thể về Qwen-Image-Edit từ chối vẽ bạo lực/máu trong phiên này; chỉ tìm được 1 discussion cũ về Qwen-Image (base, không phải Edit) bị chê "quá dễ tạo nội dung người lớn" — hướng ngược lại với giả thuyết "quá censor" — [huggingface.co/Qwen/Qwen-Image/discussions/21](https://huggingface.co/Qwen/Qwen-Image/discussions/21). Ghi **[UNVERIFIED]** cho câu hỏi Qwen-Image-Edit có làm mềm cảnh bạo lực tiên hiệp hay không — cần test thực nghiệm.
- Không tìm được thread Reddit r/comfyui hoặc r/StableDiffusion cụ thể thảo luận 2511 vs 2509 trong phiên này dù đã thử nhiều query — model khá mới (theo báo cáo trước, native ComfyUI support chỉ từ 23/09/2026) nên có thể cộng đồng Reddit chưa kịp thảo luận sâu tại thời điểm nghiên cứu (25/09/2026).
- Xianxia costume/flying sword/qi: **[UNVERIFIED]** — không có dữ liệu cộng đồng cụ thể tìm được; đây là model edit-focused (sửa ảnh có sẵn) chứ không tối ưu cho style donghua từ đầu.

## 3. Chroma1-HD (Lodestone Rock, Apache 2.0, FLUX.1-schnell base)

**Sentiment: khó đánh giá — cộng đồng nhỏ hơn hẳn, gần như không tìm được thảo luận Reddit/Civitai review cụ thể trong phiên này.**

- HF model card + local guide xác nhận: 8.9B, Apache 2.0 thuần, **uncensored, không có safety filter alignment**, retrain từ Chroma v48 để HD hơn — [huggingface.co/lodestones/Chroma1-HD](https://huggingface.co/lodestones/Chroma1-HD), [localaimaster.com/blog/chroma-local-guide](https://localaimaster.com/blog/chroma-local-guide).
- Định vị chính thức: base model để finetune, không phải sản phẩm cuối — điều này giải thích tại sao ít review "dùng thử trực tiếp" và nhiều hơn các finetune/adapter phái sinh (23 Spaces, nhiều LoRA theo báo cáo trước).
- **Không tìm được** complaint hoặc khen cụ thể nào về tay/giải phẫu/skin cho Chroma1-HD trong phiên này (đã thử nhiều query Reddit/HF) — mọi WebSearch chỉ trả về nội dung không liên quan (Wikipedia "Chroma Squad" v.v.). Đây là khoảng trống dữ liệu thật, không phải model không tồn tại vấn đề.
- Xianxia/wuxia: **không có bằng chứng cộng đồng** dùng Chroma1-HD cho thể loại này — chưa thấy LoRA hoặc checkpoint xianxia dựa trên Chroma. **[UNVERIFIED toàn bộ khía cạnh xianxia cho model này]**.
- Kết luận thực tế: sample size tìm được ~0 quote người dùng thực — không đủ cơ sở để xếp hạng sentiment định lượng, chỉ dùng được specs + định vị "base uncensored" từ nguồn chính thức.

## 4. Illustrious-XL / NoobAI-XL (SDXL finetune, anime/donghua)

**Sentiment: tích cực, hệ sinh thái lớn nhất trong nhóm** (LoRA 198K+ Illustrious, 8K+ NoobAI theo báo cáo trước) nhưng lại ít review văn bản trích được trực tiếp.

- So sánh 2 nhánh (nguồn tổng hợp aiofm.info + techtactician, không phải academic nhưng khớp hiểu biết chung cộng đồng): Illustrious = base "sạch" hơn, anatomy tốt hơn, prompt boilerplate nhẹ hơn, là trục chính của LoRA ecosystem 2026; NoobAI-XL (train tiếp trên Danbooru+e621) = tag coverage rộng hơn, đặc biệt NSFW/niche, nhưng đánh đổi 1 phần consistency — [aiofm.info/en/compare/illustrious-vs-noobai-xl](https://aiofm.info/en/compare/illustrious-vs-noobai-xl).
- 1 nguồn xếp NoobAI-XL V-Pred 1.0 là "model Illustrious-family mạnh nhất hiện tại cho NSFW anime" với tag comprehension và anatomy tốt nhất, ELO cộng đồng cao nhất — nguồn thứ cấp (airmore.ai review), chưa verify độc lập — **[UNVERIFIED mức độ chính xác ELO]**.
- Hands/anatomy: cả 2 model card đều khuyến nghị negative prompt "bad hands, mutated hands" như chuẩn baseline — xác nhận gián tiếp rằng lỗi tay vẫn là vấn đề tồn tại cần xử lý bằng negative prompt/ADetailer, không phải đã giải quyết hoàn toàn dù là SDXL đời sau.
- Xianxia/wuxia/ancient Chinese costume: tìm thấy 1 checkpoint chuyên biệt trên Civitai quảng cáo rõ "hỗ trợ không khí cổ trang Trung Hoa, trang phục lịch sử, Wuxia/Xianxia, khung hình điện ảnh" — cho thấy cộng đồng **đã tự train riêng cho nhu cầu này** thay vì dùng base model trần — [tìm thấy qua Civitai search, tên checkpoint cụ thể không xác nhận lại được trong phiên này, ghi **[UNVERIFIED — tên chính xác]**]. Không tìm được LoRA "flying sword"/"qi effect" cụ thể — khoảng trống, có thể cần search trực tiếp trên Civitai bằng tiếng Trung hoặc tag "immortal cultivation".
- Pairing: cộng đồng dùng gần như luôn kèm LoRA (đặc thù ecosystem Illustrious/NoobAI là stack nhiều LoRA phong cách + character + concept cùng lúc), không dùng base trần cho sản phẩm cuối.

## Bảng tổng hợp

| Model | Sentiment (ước lượng, độ tin cậy) | Praise chính | Complaint chính | Xianxia fit (1-5) |
|---|---|---|---|---|
| Z-Image Turbo | Tích cực ~80% (Civitai rating, trung bình tin cậy — thiếu quote Reddit gốc) | Hết plastic-skin/Flux-chin, nhanh (2-8s), không safety filter cứng | Vẫn lỗi giải phẫu pose phức tạp, text rendering yếu hơn Qwen | 2 — base photoreal, không có LoRA/ecosystem xianxia riêng |
| Qwen-Image-Edit-2511 | Tích cực (nguồn blog/HF, thấp tin cậy — chưa thấy Reddit thảo luận vì quá mới) | Character consistency tốt hơn 2509 rõ rệt, giảm image drift, group-shot tốt | Chưa rõ mức độ censor bạo lực; ít dữ liệu cộng đồng do mới ra | 2 — tool edit, không tối ưu style donghua từ đầu |
| Chroma1-HD | Không đủ dữ liệu để đánh giá (gần 0 quote tìm được) | Apache 2.0 thuần, uncensored, nền tốt để finetune | Thiếu bằng chứng chất lượng thực tế từ người dùng | 1 (chưa xác minh) — không có bằng chứng dùng cho xianxia |
| Illustrious-XL / NoobAI-XL | Tích cực, ecosystem lớn nhất (LoRA 198K/8K, trung bình tin cậy — thiếu quote trực tiếp) | Anatomy/consistency tốt (Illustrious), tag coverage rộng (NoobAI), LoRA khổng lồ | Tay vẫn cần negative prompt/ADetailer; NoobAI đánh đổi consistency lấy coverage | 4 — có checkpoint/LoRA chuyên cổ trang Trung Hoa, cộng đồng đã tự đáp ứng nhu cầu này |

## Khuyến nghị cho pipeline (3-5 dòng)

Giữ **Z-Image Turbo làm engine bulk scene tốc độ cao** (đúng vai trò hiện tại) nhưng thêm ADetailer/hand-fix pass vì cộng đồng + chính team Tongyi xác nhận lỗi giải phẫu pose phức tạp chưa giải quyết hết. Dùng **Illustrious-XL/NoobAI-XL + LoRA cổ trang chuyên biệt** cho phần lớn scene xianxia/wuxia thật sự (nhân vật mặc cổ phục, phi kiếm, hiệu ứng khí) vì đây là model duy nhất trong 4 có bằng chứng cộng đồng đã giải quyết nhu cầu này cụ thể — Z-Image/Qwen/Chroma đều thiếu ecosystem cho thể loại này. Nâng **Qwen-Image-Edit-2511** cho các shot cần giữ nhân vật nhất quán qua nhiều cảnh (character consistency là điểm mạnh xác nhận rõ nhất của bản này) thay vì 2509. **Chưa nên đưa Chroma1-HD vào pipeline chính thức** — thiếu bằng chứng thực tế đủ để đánh giá chất lượng, chỉ nên thử nghiệm nhỏ cho scene bạo lực bị Qwen/Illustrious chặn, và cần tự test thay vì dựa vào cộng đồng vì gần như không có dữ liệu người dùng công khai.

## Câu hỏi chưa giải quyết

1. Không truy cập được Reddit trực tiếp trong phiên này (WebFetch bị chặn) — mọi claim "Reddit nói" đều qua nguồn thứ cấp; nên thử lại bằng trình duyệt thật hoặc Reddit API nếu cần quote/link gốc chính xác.
2. Không tìm được thảo luận Bilibili/Zhihu/LiblibAI nào — phần đánh giá xianxia cho Illustrious/NoobAI hoàn toàn dựa trên Civitai tiếng Anh, có thể thiếu insight từ cộng đồng Trung Quốc vốn hiểu rõ thẩm mỹ cổ trang hơn.
3. Chroma1-HD gần như không có dữ liệu người dùng công khai tìm được — cần tự test thực nghiệm (đã ghi nhận là unresolved từ báo cáo trước, vẫn chưa giải quyết).
4. Chưa xác nhận được Qwen-Image-Edit-2511 có bị safety filter làm mềm cảnh bạo lực/máu tiên hiệp hay không — cần test prompt cụ thể thay vì tìm forum (model quá mới, cộng đồng chưa thảo luận đủ).
5. Chưa tìm được LoRA "flying sword"/"qi effect" cụ thể cho Illustrious/NoobAI — cần search trực tiếp Civitai bằng từ khóa tiếng Trung (仙侠/御剑/灵气) thay vì tiếng Anh.

Status: DONE_WITH_CONCERNS
