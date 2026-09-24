# Research: chất lượng narration/voice cho kênh audio-story YouTube (xianxia/AI story), lựa chọn TTS

Status: DONE_WITH_CONCERNS — bằng chứng trực tiếp từ kênh cụ thể (WPM đo, tỉ lệ nhạc nền/không) hạn chế, một số claim đánh dấu unknown.

## 1. Bằng chứng từ kênh thực tế

**Kênh xianxia/webnovel EN trên YouTube:** tìm thấy kênh "XianXia Audio Books" và kênh audiobook wuxia/xianxia EN khác nhưng không truy cập được thống kê nhạc nền/pacing cụ thể qua search — **unknown**, cần nghe trực tiếp để xác nhận giả thuyết "không nhạc nền" của user. Điểm đáng chú ý: audiobook xianxia **có bản quyền trên Audible** (Cultivation Is a Game — Jason Vu; Modern Patriarch — Ralph Lister; The Seven Realms Cultivator — Daniel Byshenk) đều dùng **narrator con người**, không phải TTS — cho thấy thị trường audiobook trả phí vẫn ưu tiên giọng người thật. Kênh YouTube free-tier (không qua Audible) mới là nơi TTS/AI voice phổ biến hơn.

**Kênh "reddit story"/AI story EN:** ElevenLabs được xác nhận là TTS phổ biến nhất cho dạng nội dung này — "hầu hết kênh faceless/AI chạy trên ElevenLabs". Một creator dùng hoàn toàn ElevenLabs đạt 6k+ sub và ~8 triệu view trong ~3 tháng (nguồn: nerdynav.com review, không kiểm chứng độc lập được số liệu — **unknown mức độ chính xác**). Voice phổ biến: "David Castlemore" cho mystery/thriller, "Aaron" phổ biến với kênh AI/tech. Không tìm thấy dữ liệu định lượng về WPM hay giới tính giọng chiếm ưu thế — **unknown**.

**Kênh truyện audio tu tiên tiếng Việt:** các kênh như "Truyện Audio", "Kho Truyện Audio" dùng **giọng đọc AI + có nhạc nền/sound effect** (theo mô tả các trang liệt kê kênh) — trái ngược giả thuyết user cho nhánh EN. Đây là tín hiệu gián tiếp rằng ở thị trường VI, nhạc nền vẫn phổ biến; **chưa kiểm chứng trực tiếp bằng cách nghe kênh** nên chỉ nêu như xu hướng quan sát được qua mô tả bên thứ ba.

**Phản ứng khán giả với giọng AI:** có bằng chứng phản đối chung chung (Goodreads: người nghe "zero interest" với sách chuyển từ text sang AI voice, lo giọng máy "không hấp dẫn") nhưng không tìm được thread Reddit cụ thể phàn nàn về kênh xianxia/story cụ thể — **unknown, không có dẫn chứng định lượng**.

**Kết luận cho câu hỏi 1:** không đủ bằng chứng trực tiếp để xác nhận hay bác bỏ giả thuyết "kênh EN thành công không dùng nhạc nền" — cần nghe mẫu 5-10 kênh top thực tế (đề xuất bổ sung nếu cần độ chắc chắn cao hơn). Bằng chứng gián tiếp mạnh nhất: narrator con người vẫn là chuẩn cho audiobook trả phí (Audible), TTS phổ biến ở kênh YouTube free/faceless, ElevenLabs là TTS "nhận diện được" nhiều nhất trong niche reddit-story.

## 2. Yếu tố giữ chân người nghe trong narration

Dựa trên nguồn về pacing narration nói chung (narrationbox.com, promptvo.com, writersaudiobookclinic.com):

- **Tốc độ (WPM):** mức tối ưu chung 145-165 WPM; thể loại giật gân/action có thể lên 180 WPM, fiction văn học 130-150, nội dung giáo dục 120-135 (retention tăng khi chậm hơn). Với xianxia (nhiều thuật ngữ tu luyện, thoại đối kháng, cao trào chiến đấu) nên nằm khoảng 150-170 WPM, chậm lại còn ~120-130 ở đoạn giải thích cảnh giới/hệ thống tu luyện phức tạp.
- **Pause:** pause ngắn tăng độ rõ, pause trung bình tăng kịch tính, pause dài chuyển trọng lượng cảm xúc — cần đặt có chủ đích ở chuyển cảnh/nhân vật/tâm trạng, không đều đều.
- **Cảm xúc:** kết hợp tốc độ + vị trí pause tạo "emotional pacing" — nguồn cho biết nghiên cứu về auditory processing ghi nhận retention tăng khi có tonal cues tự nhiên (con số "40%" từ 1 nguồn thứ cấp, chưa kiểm chứng độc lập — **unknown độ tin cậy**).
- **Phát âm thuật ngữ Trung Quốc/cảnh giới tu tiên:** không tìm được nguồn trực tiếp bàn về việc audiobook narrator xử lý tên cảnh giới (Trúc Cơ, Kim Đan, Nguyên Anh...) — nhưng đây là rủi ro thực tế đã biết trong ngành dịch thuật/TTS: phiên âm sai hoặc **không nhất quán qua nhiều giờ/nhiều tập** phá vỡ trải nghiệm. Đây là điểm TTS cục bộ có lợi thế kiểm soát được (pronunciation dictionary) hơn giọng người thuê ngoài không quen thuật ngữ.
- **Nhất quán nhiều giờ:** với video 30 phút-3 giờ, timbre/tốc độ/prosody phải ổn định — đây chính là tiêu chí "long-form stability" ở mục 3, không phải property chung của mọi TTS.

## 3. So sánh TTS cho narration dài (EN, 2026)

### Local/open-weight

| Model | Naturalness (Arena Elo, thời điểm) | Emotion control | Multi-speaker | Long-form stability | Voice cloning | License | VRAM/tốc độ trên 5060 Ti 16GB (ước tính) |
|---|---|---|---|---|---|---|---|
| **Chatterbox (+ Turbo/Multilingual)** | 1021, #70 (Artificial Analysis, phiên bản May-2025) | Cơ bản, ổn định qua "alignment-informed inference" | Không (single-speaker per call) | Cao ("ultra-stable", train trên 0.5M giờ data) | Có (zero-shot vài giây ref audio) | **MIT** | 2-3GB VRAM, RTF <1, latency <300ms — chạy thoải mái trên 16GB |
| **Higgs Audio v2 (v3 TTS)** | 1033, #63 (Jun-2026 bản v3) | Tốt, train trên >10M giờ audio | Có (multi-speaker dialogue) | Tốt, gần bằng SparkTTS, hơn IndexTTS 1.5/CosyVoice2/Chatterbox theo 1 nguồn | Có | **Apache 2.0** | Nặng hơn Chatterbox, có báo cáo chạy chậm/phức tạp trên GPU 8GB — 16GB đủ nhưng cần benchmark thực tế (unknown số cụ thể) |
| **IndexTTS2** | Chưa có Elo công khai tìm được | Mạnh — "emotion-timbre separation" tách biệt cảm xúc/âm sắc, one of best cho long-form narration theo 1 nguồn (instavar.com) | Không rõ | Cải thiện qua 3-stage training, tốt cho long-form theo benchmark riêng | Có (zero-shot) | **Unknown** — không xác nhận được license qua search | Unknown, chưa có số cụ thể |
| **Kokoro (82M)** | 1061, #49 (Jan-2025) | Hạn chế (model nhỏ) | Không | Khá cho model nhỏ nhưng không phải build cho narration dài | Hạn chế | **Apache 2.0** | Cực nhẹ (82M params), chạy nhanh trên mọi GPU kể cả 5060 Ti, giá tương đương $0.65/1M ký tự nếu host |
| **Orpheus 3B** | Không có trên leaderboard đã kiểm | Có emotion tag hướng dẫn | Unknown | Unknown | Zero-shot | **Apache 2.0** | 3B param, cần benchmark, khả năng chạy tốt trên 16GB |
| **Dia (Dia2)** | Không có trên leaderboard | Unknown | Có hỗ trợ hội thoại 2 giọng theo thiết kế gốc | Unknown | Unknown | **Apache 2.0** | Unknown |
| **VibeVoice (Microsoft)** | Không có trên leaderboard công khai | Tốt, thiết kế cho podcast/audiobook | **Có, tới 4 giọng, 90 phút liên tục** — mạnh nhất cho multi-character narration | Thiết kế riêng cho long-form (90 phút) | Có | **MIT** nhưng **Microsoft đã gỡ code VibeVoice-TTS khỏi repo sau khi phát hiện bị lạm dụng, và ghi rõ "không khuyến nghị dùng trong ứng dụng thương mại/thực tế"** — rủi ro pháp lý/uy tín dù license kỹ thuật cho phép | Unknown tốc độ cụ thể |
| **Sesame CSM (1B)** | Không nổi bật trong benchmark tổng quát; ElevenLabs/Cartesia thường xếp trên về realism | Conversational-focused, không chuyên narration dài | Có (hội thoại) | Unknown cho long-form | Có | **Apache 2.0** | 1B, nhẹ, chạy tốt trên 16GB |
| **Fish Speech/OpenAudio (Fish Audio S2 Pro)** | 1120, #27 (Mar-2026) — cao nhất trong nhóm open-weight kiểm được ngoài Breeze | Unknown chi tiết | Unknown | Định vị cho multilingual, chưa xác nhận long-form narration cụ thể | Có | Unknown chính xác (cần xác nhận thêm, nhiều nguồn không nêu rõ) | Unknown |
| **MOSS-TTSD** | Không có trên leaderboard đã kiểm | Thiết kế cho dialogue synthesis, voice design | Có (dialogue-focused family) | Unknown | Unknown | **Apache 2.0** | Unknown |
| **Breeze TTS 2** (mới, ngoài danh sách user hỏi nhưng đáng chú ý) | **1204-1215, #9 — open-weight cao nhất, vượt cả ElevenLabs v3 (1177)** (Aug-2026) | Unknown chi tiết | Unknown | Unknown | Unknown | Unknown license | Đáng điều tra thêm — mới release |

### Paid/API

| Model | Elo/rank (thời điểm) | Emotion | Multi-speaker | Long-form | Cloning | Giá |
|---|---|---|---|---|---|---|
| **ElevenLabs v3** | 1167-1196, #12-17 (Feb/Aug-2026) — không còn #1, bị Cartesia Sonic 3.6 (1273), Gemini 3.8 Flash TTS (1260), Qwen-Audio-3.0-TTS-Plus (1259), Breeze TTS 2 (1204) vượt | Tốt, có audio tags điều khiển cảm xúc | Có (dialogue mode) | Tốt, dùng rộng rãi cho audiobook creator tools | Có (voice cloning chuyên nghiệp) | ~$0.10/1000 ký tự (Multilingual), $0.05/1000 (Flash/Turbo). Gói Creator $22/tháng = 100k ký tự. **Ước tính**: 1 giờ audio ~9000 từ ~50k ký tự → ~$5/giờ (Multilingual) hoặc ~$2.5/giờ (Flash) |
| **OpenAI gpt-4o-mini-tts** | Không trên Arena riêng | Có, prompt-based style control | Unknown rõ multi-speaker | Unknown | Không hỗ trợ cloning giọng tùy ý (giọng preset) | ~$0.015/phút → **~$0.9/giờ** — rẻ nhất trong nhóm paid |
| **Google Gemini TTS** | Gemini 3.8 Flash TTS: 1260, #2 trên Arena (rất cao) | Unknown chi tiết | Unknown | Unknown | Unknown | Gemini 2.5 Flash TTS ~$0.015/phút (~$0.9/giờ), Gemini 3.1 Flash TTS preview ~$0.03/phút (~$1.8/giờ) |
| **Hume Octave** | Không trên Arena chính | **Chuyên biệt cảm xúc/prosody fine-grained** — điểm mạnh nhất nhóm paid cho emotion control | Unknown | Unknown | Unknown | $50-100/1M ký tự (Pro) — đắt, ~50k ký tự/giờ → **~$2.5-5/giờ** |
| **Cartesia Sonic 3.6** | **1273, #1 trên Arena** (thời điểm search) | Unknown chi tiết nhưng dẫn đầu naturalness | Unknown | Thiết kế cho low-latency real-time, chưa rõ ổn định nhiều giờ | Unknown | ~$0.03/phút (~$1.8/giờ) hoặc Sonic 3 ~$35/1M ký tự (~$1.75/giờ ở 50k ký tự) |

**Đọc bảng cẩn thận:** Elo/rank thay đổi liên tục trong 2026 (nguồn ghi rõ "rankings have been changing frequently"), ElevenLabs KHÔNG còn #1 tại thời điểm nghiên cứu (Cartesia, Gemini, Qwen, thậm chí Breeze TTS 2 mã nguồn mở đều vượt) — nhưng ElevenLabs vẫn có sample size lớn nhất (3753 lượt) nên độ tin cậy thống kê cao hơn các model mới nổi ít dữ liệu.

## 4. TTS tiếng Việt

- **VieNeu-TTS v3 Turbo**: **Apache 2.0**, open-source, voice cloning tức thời, chạy CPU real-time, 24-48kHz tùy fork. Phù hợp license thương mại cho dự án này (đã ghi nhận trong memory dự án).
- **VieNeu-TTS v4**: mạnh hơn về cloning nhưng **đã đóng nguồn, chỉ qua API trả phí (vieneu.io)** — không dùng được nếu cần local/miễn phí.
- Không tìm thấy so sánh sâu VieNeu với các TTS VI khác (chưa có nguồn) — **unknown**, cần tìm thêm nếu cần đối chiếu (ví dụ so với viXTTS — đã biết non-commercial theo memory dự án trước, hoặc VITS-based tiếng Việt khác).

## 5. Chính sách YouTube về giọng AI

- YouTube 2026 yêu cầu **disclosure** (tick "altered or synthetic content") cho video có giọng tổng hợp/nhân vật deepfake/kịch bản AI viết chính. **Ngoại lệ quan trọng: KHÔNG cần disclosure nếu nội dung rõ ràng là "stylized/non-realistic", bao gồm TTS chuẩn dùng giọng generic không giả mạo người thật** — đây là trường hợp phù hợp với kênh audio-story dùng TTS giọng tổng hợp không nhái giọng người nổi tiếng.
- Vi phạm (không disclose khi cần) áp dụng hệ thống 3 lần: cảnh báo → tạm ngưng kiếm tiền 90 ngày → xóa vĩnh viễn khỏi YPP.
- 2026: chính sách "repetitious content" đổi tên thành "inauthentic content", mở rộng phạm vi sang **kênh xây dựng trên template hàng loạt, clip tái sử dụng, slideshow không có narrative, script đọc nguyên văn từ nguồn ngoài** — đây là rủi ro thực sự cho kênh audio-story AI generic, không phải riêng việc dùng giọng AI. Không tìm được báo cáo demonetization cụ thể chỉ vì dùng TTS rõ ràng (obviously-TTS) mà tuân thủ disclosure — **unknown, chưa có case study xác nhận**.
- Khuyến nghị: luôn tick disclosure cho an toàn dù kỹ thuật có thể miễn, và tránh outline/kịch bản đọc nguyên văn 1:1 từ nguồn khác (rủi ro "inauthentic content" cao hơn rủi ro giọng AI).

## Khuyến nghị

**Default local EN voice stack:** **Chatterbox (MIT)** làm nền tảng chính — license sạch nhất, VRAM nhẹ nhất (2-3GB, dư dả trên 16GB), voice cloning zero-shot, đủ ổn định cho narration. Bổ sung **Orpheus 3B (Apache 2.0)** cho các đoạn cần emotion tag rõ rệt (cao trào chiến đấu, đối thoại kịch tính) nếu Chatterbox thiếu sắc thái. Theo dõi **IndexTTS2** và **Breeze TTS 2** — cả hai có tín hiệu chất lượng cao nhưng license/benchmark chưa xác nhận đủ để đưa vào default ngay; xác nhận license trước khi cân nhắc.

Không khuyến nghị **VibeVoice** dù license MIT và tính năng multi-speaker 90 phút hấp dẫn nhất cho per-character voice, vì chính Microsoft khuyến cáo không dùng thương mại và đã gỡ code TTS — rủi ro không tương xứng với dự án monetized.

**Premium option:** **ElevenLabs v3** vẫn là lựa chọn an toàn nhất về mặt "recognized quality" và feature (audio tags cảm xúc, dialogue mode) dù không còn #1 Arena — ước tính ~$2.5-5/giờ audio. Nếu ưu tiên giá rẻ mà chất lượng cao, **Gemini Flash TTS** (~$0.9-1.8/giờ) hoặc **gpt-4o-mini-tts** (~$0.9/giờ) là phương án backup rẻ hơn nhiều, đổi lại ít kiểm soát cloning giọng riêng.

**Nhạc nền:** khuyến nghị **để làm toggle tùy chọn, mặc định TẮT** — phù hợp hướng "narration-first" mà user giả định, và tránh rủi ro pháp lý/DMCA từ nhạc nền không rõ nguồn. Tuy nhiên bằng chứng thu thập được **không đủ mạnh để xác nhận đây là chuẩn ngành cho nhánh EN** (chưa nghe trực tiếp kênh top); nhánh VI (tu tiên) dường như phổ biến nhạc nền nhẹ theo mô tả bên thứ ba. Để toggle mặc định tắt là quyết định an toàn/tối giản (ít phụ thuộc, ít rủi ro bản quyền) hơn là kết luận dựa trên dữ liệu thị trường chắc chắn.

**Pipeline features nên có để cải thiện narration:**
1. **Pronunciation dictionary** cho thuật ngữ tu tiên (Trúc Cơ/Foundation Establishment, Kim Đan/Core Formation, Nguyên Anh/Nascent Soul...) — map cố định EN, tránh TTS tự phát âm sai hoặc không nhất quán giữa các tập.
2. **SSML/emotion tag** (Chatterbox không hỗ trợ SSML chuẩn — cần hoặc dùng Orpheus's emotion tags, hoặc post-process prosody bằng markup riêng của pipeline).
3. **Per-character voice** (đổi giọng theo nhân vật ở đoạn hội thoại) — cân nhắc dùng nhiều Chatterbox voice profile khác nhau (multiple zero-shot clones) thay vì phụ thuộc VibeVoice.
4. **Chunking strategy**: chia script theo câu/đoạn (không theo ký tự cố định) để giữ ngữ điệu tự nhiên qua ranh giới chunk, có cơ chế crossfade/silence-trim giữa các chunk để giọng liền mạch qua video dài 30 phút-3 giờ — đây là điểm rủi ro drift lớn nhất cho local TTS chạy hàng giờ, cần test thực nghiệm (chưa có benchmark drift cụ thể tìm được — unknown).
5. **QA tự động**: kiểm tra WPM trung bình theo đoạn (mục tiêu 150-170 WPM cho action, 120-130 cho giải thích hệ thống tu luyện) và phát hiện đoạn lệch tốc độ bất thường (dấu hiệu artifact/drift của TTS).

## Câu hỏi chưa giải quyết

1. Cần nghe trực tiếp 5-10 kênh xianxia/AI-story EN top thực tế để xác nhận/bác bỏ giả thuyết "không nhạc nền" — search không cung cấp đủ bằng chứng nghe được.
2. License chính xác của IndexTTS2 và Fish Speech/OpenAudio chưa xác nhận được qua search — cần kiểm tra trực tiếp GitHub/HuggingFace repo trước khi dùng thương mại.
3. Benchmark tốc độ/VRAM thực tế trên RTX 5060 Ti 16GB (Blackwell sm_120) cho Higgs Audio v2, IndexTTS2, Orpheus, Dia chưa có số liệu công khai — cần tự benchmark.
4. Chưa có case study cụ thể xác nhận hoặc bác bỏ việc demonetization xảy ra với kênh dùng TTS rõ ràng dù đã disclosure đúng quy định.
5. Breeze TTS 2 (open-weight, Elo cao nhất nhóm mã nguồn mở, vượt ElevenLabs v3) là phát hiện mới ngoài phạm vi câu hỏi gốc — đáng điều tra riêng về license/VRAM trước khi đưa vào stack chính thức.
