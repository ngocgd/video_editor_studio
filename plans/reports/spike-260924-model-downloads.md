# Spike: first model download batch (Loomtale Studio)

Date: 2026-09-24/25 (Asia/Bangkok) · User approval: "cài 30gb download luôn đi" · Anonymous HF access, no token used.

## Method
- Licence, revision, gated flag and LFS sha256 resolved from `https://huggingface.co/api/models/<repo>?blobs=true` (`cardData.license` + `license:*` tag) before any download.
- Volume `loomtale_models` (not `models` as phase 1b says; the task named it `loomtale_models`). Mountpoint: `/var/lib/docker/volumes/loomtale_models/_data`.
- Throwaway `python:3.12-slim` container, `huggingface_hub.hf_hub_download(repo, path, revision=<sha>, token=False)`: staged in the volume, moved into the ComfyUI layout, then sha256 computed inside the container and compared with the HF LFS sha256. The script refuses any file that is not `.safetensors`, `.gguf` or `.json` (tokenizer). Nothing downloaded was executed. `/models/.staging` was removed after the run.
- Two interruptions, both resumed: the first container was SIGKILLed (exit 137) after a DNS failure, probably a Docker Desktop restart. The second run hit a pip timeout on PyPI. The third run, with retries, completed. Files already in place were checked again by sha256 and not downloaded again.

## Licences (verified)
| Model | Upstream licence | URL |
|---|---|---|
| Qwen-Image-Edit-2509 (base, `Qwen/Qwen-Image-Edit-2509` @ d3968ef930e841f4c73640fb8afa3b306a78167e) | Apache-2.0 | https://huggingface.co/Qwen/Qwen-Image-Edit-2509 |
| QuantStack GGUF repack | Apache-2.0 | https://huggingface.co/QuantStack/Qwen-Image-Edit-2509-GGUF |
| Comfy-Org Qwen-Image repack (TE + VAE) | Apache-2.0 | https://huggingface.co/Comfy-Org/Qwen-Image_ComfyUI |
| Z-Image Turbo (base `Tongyi-MAI/Z-Image-Turbo` @ f332072aa78be7aecdf3ee76d5c247082da564a6) | Apache-2.0 | https://huggingface.co/Tongyi-MAI/Z-Image-Turbo |
| Comfy-Org Z-Image repack | Apache-2.0 | https://huggingface.co/Comfy-Org/z_image_turbo |
| Chatterbox | MIT | https://huggingface.co/ResembleAI/chatterbox |

None of these repos is gated.

## Files
| Model | Task | Licence | Repo | Revision | Path in volume | Size (bytes) | sha256 | Status |
|---|---|---|---|---|---|---|---|---|
| Qwen-Image-Edit-2509 Q4_K_M | char sheets / ref edit | Apache-2.0 | QuantStack/Qwen-Image-Edit-2509-GGUF | 84a3006979126011422eeeefe0c9485ddf431ef5 | /models/diffusion_models/Qwen-Image-Edit-2509-Q4_K_M.gguf | 13065746976 | 08f27cdf3e760edef5136ab0afdb9d3ed7a2799bd730b8d5cd9ecb291d808425 | verified |
| Qwen2.5-VL-7B TE fp8 | Qwen-Image(-Edit) text encoder | Apache-2.0 | Comfy-Org/Qwen-Image_ComfyUI | 1f12b17be14c89b026c51a91d67c32f84bb047bc | /models/text_encoders/qwen_2.5_vl_7b_fp8_scaled.safetensors | 9384670680 | cb5636d852a0ea6a9075ab1bef496c0db7aef13c02350571e388aea959c5c0b4 | verified |
| Qwen-Image VAE | Qwen-Image(-Edit) VAE | Apache-2.0 | Comfy-Org/Qwen-Image_ComfyUI | 1f12b17be14c89b026c51a91d67c32f84bb047bc | /models/vae/qwen_image_vae.safetensors | 253806246 | a70580f0213e67967ee9c95f05bb400e8fb08307e017a924bf3441223e023d1f | verified |
| Z-Image Turbo int8 (convrot) | scenes txt2img | Apache-2.0 | Comfy-Org/z_image_turbo | 6fc90a3b1b653e935a0d175e260736de25b84df5 | /models/diffusion_models/z_image_turbo_int8_convrot.safetensors | 6201001296 | be517ebd47c912a5626a588e1aeea43e6be4a43c0cdcd2b48a2a780d9f358635 | verified |
| Qwen3-4B TE fp8_mixed | Z-Image text encoder | Apache-2.0 | Comfy-Org/z_image_turbo | 6fc90a3b1b653e935a0d175e260736de25b84df5 | /models/text_encoders/qwen_3_4b_fp8_mixed.safetensors | 5631994051 | 72450b19758172c5a7273cf7de729d1c17e7f434a104a00167624cba94f68f15 | verified |
| Z-Image VAE (ae) | Z-Image VAE | Apache-2.0 | Comfy-Org/z_image_turbo | 6fc90a3b1b653e935a0d175e260736de25b84df5 | /models/vae/ae.safetensors | 335304388 | afc8e28272cd15db3919bacdb6918ce9c1ed22e96cb12c4d5ed0fba823529e38 | verified |
| Chatterbox voice encoder | TTS (EN) | MIT | ResembleAI/chatterbox | 5bb1f6ee58e50c3b8d408bc82a6d3740c2db6e18 | /models/tts/chatterbox/ve.safetensors | 5695784 | f0921cab452fa278bc25cd23ffd59d36f816d7dc5181dd1bef9751a7fb61f63c | verified |
| Chatterbox T3 | TTS (EN) | MIT | ResembleAI/chatterbox | 5bb1f6ee58e50c3b8d408bc82a6d3740c2db6e18 | /models/tts/chatterbox/t3_cfg.safetensors | 2129653744 | 914cb1696f47527fe8852ca8f1fe1fa63cb34f76f9c715e84e067b744dd0da81 | verified |
| Chatterbox S3Gen | TTS (EN) | MIT | ResembleAI/chatterbox | 5bb1f6ee58e50c3b8d408bc82a6d3740c2db6e18 | /models/tts/chatterbox/s3gen.safetensors | 1056484620 | 2b78103c654207393955e4900aac14a12de8ef25f4b09424f1ef91941f161d4e | verified |
| Chatterbox tokenizer | TTS (EN) | MIT | ResembleAI/chatterbox | 5bb1f6ee58e50c3b8d408bc82a6d3740c2db6e18 | /models/tts/chatterbox/tokenizer.json | 25470 | d71e3a44eabb1784df9a68e9f95b251ecbf1a7af6a9f50835856b2ca9d8c14a5 | recorded (not an LFS file, so HF has no sha256 to compare against) |

**Total: 38,064,383,255 bytes (≈38.06 GB / 35.45 GiB), 10 files.** `du -sb /models` gives the same total.

No files are shared between the models. Qwen uses qwen_2.5_vl_7b + qwen_image_vae, and Z-Image uses qwen_3_4b + the Flux-style `ae`.

## Choices and skipped items
- **Qwen-Image-Edit 2509** was chosen over 2511. Both are Apache-2.0, and 2509 has the pinned QuantStack Q4_K_M GGUF the task asked for.
- **Z-Image diffusion model: int8_convrot (6.2GB) instead of bf16 (12.3GB).** The repo has no fp8 file. bf16 would bring the total to about 44GB, above the ~40GB cap, and would crowd out Chatterbox. It also exceeds the ≈10.9GB VRAM budget in phase 9a. int8 has an official ComfyUI workflow (`image_z_image_turbo_int8.json`). **Deviation from the "fp8 or bf16" preference:** bf16 can be added later (12.3GB, sha 2407613050b809ffdff18a4ac99af83ea6b95443ecebdf80e064a79c825574a6).
- **Z-Image text encoder: fp8_mixed (5.6GB) instead of bf16 `qwen_3_4b` (8.0GB)**, for size and VRAM.
- Skipped: Chatterbox `conds.pt`, `*.pt` (pickle files, refused). Without `conds.pt` there is no built-in default voice, so Chatterbox needs a reference audio clip for each voice.
- Skipped: Chatterbox multilingual files (`t3_mtl23ls_v2/v3`, `t3_23lang`, `s3gen_v3`, mtl tokenizers). They are out of scope: the EN set only.
- Skipped: the Z-Image distill-patch LoRA, the Qwen-Image base (thumbnails, phase 9a) and every other quant.
- Excluded by policy: Qwen-Image-2.1 (non-commercial), Flux dev / Kontext dev.

## Disk (C:)
- Before: 214,641,627,136 B free (≈199.9 GiB).
- Mid-run: 312,634,527,744 B. Something outside this task freed about 98GB, at the same time as the Docker restart or kill.
- After: 276,207,415,296 B free (≈257.2 GiB). Because of that outside change, before minus after does not measure this download.

## Unresolved questions
- Is the int8 Z-Image model OK, or should bf16 be added (+12.3GB)? The pinned ComfyUI commit must support int8_convrot. Check this in phase 1b/9a.
- The phase 1b and 9a docs name the volume `models`, but the actual volume is `loomtale_models`. The plan or compose file needs to match.
- What freed about 98GB on C: and restarted or killed Docker mid-run? It was not this task.

## Batch 2 (2026-09-25): Qwen-Image-Edit-2511, Illustrious-XL, xianxia LoRA

Approved by the user in the task brief (download the 2511 GGUF, then delete 2509 after a passing smoke; Illustrious-XL plus one xianxia LoRA with commercial image rights; budget ≤25GB). Same method as batch 1: a throwaway `python:3.12-slim` container and anonymous `hf_hub_download(..., revision=<sha>, token=False)`. The Civitai file was fetched over plain HTTPS with no account or token. Files were staged in `/models/.staging`, and sha256 was computed inside the container and compared with the HF LFS sha256 or the Civitai API `hashes.SHA256` before each file was moved into place. The script refuses anything other than `.safetensors`/`.gguf`. `/models/.staging` was removed after the run.

| Model | Task | Licence | Repo / source | Revision / version id | Path in volume | Size (bytes) | sha256 | Status |
|---|---|---|---|---|---|---|---|---|
| Qwen-Image-Edit-2511 Q4_K_M | char sheets / ref edit | Apache-2.0 (repo + base `Qwen/Qwen-Image-Edit-2511` @ 6f3ccc0b56e431dc6a0c2b2039706d7d26f22cb9) | unsloth/Qwen-Image-Edit-2511-GGUF | 0d33d9692b4b26212297240d87b0d4719aa4fd06 | /models/diffusion_models/qwen-image-edit-2511-Q4_K_M.gguf | 13244758624 | 8677bac90627adbbc11efab87b1870e701c4eb3689ee865a3de8ab81b705a723 | verified; smoke passed |
| Illustrious-XL v1.1 | anime/donghua txt2img (SDXL) | SDXL licence (CreativeML Open RAIL++-M), per the repo's `license_link` | OnomaAIResearch/Illustrious-XL-v1.1 | 8d966ec810874502d56a22ec9130dab6ef74c5ff | /models/checkpoints/Illustrious-XL-v1.1.safetensors | 6938040728 | 536863e9f0c13b0ce834e2f8a19ada425ee4f722c0ad3d0051ec7e6adaa8156c | verified |
| Xianxia Art Style (LoRA, Illustrious) | xianxia style | Civitai permissions: commercial use Image/Rent/RentCivit/Sell/SellMerge, no credit required | https://civitai.com/models/1955005 (creator dfdfr232) | modelVersion 2212667, fileId 2109022 | /models/loras/Xianxia_Art_Style.safetensors | 228462452 | d2608c08cb73422447316f60753cce2f7f9b3166496abf9585c9adb77140570f | verified (Civitai pickle/virus scan: Success) |

**Batch 2 total: 20,411,261,804 bytes (≈20.41 GB), 3 files.** This is under the 25GB budget.

### Why these sources
- **The 2511 GGUF comes from unsloth, not QuantStack.** `QuantStack/Qwen-Image-Edit-2511-GGUF` does not exist on HF: the API returns a not-found/401 error. unsloth has the most-used 2511 GGUF (≈413k downloads). It is Apache-2.0 with `base_model: Qwen/Qwen-Image-Edit-2511`, and its card says it was built with city96's ComfyUI-GGUF tooling. It is a real 2511 quant: the GGUF metadata shows `general.architecture=qwen_image`, `file_type=15` (Q4_K_M) and 1934 tensors. It also contains the `__index_timestep_zero__` marker tensor, which ComfyUI's `model_detection.py` uses to recognise 2511.
- **Illustrious-XL v1.1 was chosen over v0.1.** v0.1 (`Illustrious-xl-early-release-v0`) is under FAIPL-1.0-SD. That licence claims no rights over outputs, but the v0.1 card calls the model "research-only purpose" and says it "discourages the usage of model over monetization purpose". v1.0 and v1.1 are official OnomaAI releases under the SDXL licence, which says "Licensor claims no rights in the Output You generate" and limits use only through the Attachment A restrictions. v1.1 has the higher ELO (1617 vs 1571 for v1.0, per its card), and the v1.0 card says v0.1 LoRAs work natively. **Monetized YouTube use of the outputs is allowed**, subject to the Attachment A use restrictions. v2.0 was not chosen because it only carries a `creativeml-openrail-m` tag and has no licence file in the repo.
- **LoRA: "Xianxia Art Style" V1** (trigger `xianxia style`; 787 downloads and 80 up / 0 down votes as of 2026-09-25). It was the only xianxia *style* LoRA for Illustrious I found (not a character/IP LoRA) whose permissions include commercial use of generated images. Skipped:
  - `漢服/Hanfu` (441397): the most popular, with 6.3k downloads, but its commercial use is `RentCivit` only, with no `Image`.
  - `CC's Chinese Fantasy`: no `Image` permission.
  - Character LoRAs from donghua IPs: copyright risk.

  The anonymous download worked (a 307 redirect to a signed B2 URL), so no token was used.

### Deleted (user-approved)
- `/models/diffusion_models/Qwen-Image-Edit-2509-Q4_K_M.gguf` (13,065,746,976 B, sha 08f27cdf…d808425) was deleted after the 2511 smoke passed. Nothing else was deleted. `comfyui/workflows/qwen-image-edit-2509-smoke.json` was removed, and `scripts/comfyui-smoke-spike.sh` now points at `qwen-image-edit-2511-smoke.json`.
- Volume after: `du -sb /models` = 45,409,930,851 B. The expected figure is 38,064,383,255 + 20,411,261,804 − 13,065,746,976 = 45,409,898,083 B; the ~33KB difference is new directory entries.

### Disk (C:)
- Before batch 2: 232,470,863,872 B free. After the download and the deletion: 215,498,772,480 B free (−16.97 GB).
- The WSL2 vhdx does not hand the ~13GB freed by deleting 2509 back to Windows until the disk is compacted. That space can still be reused inside the VM.
