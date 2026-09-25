"""Unit tests for the weights-format safety guard."""

from __future__ import annotations

import pytest

from loomtale_worker.weights import UnsafeWeightsFormatError, assert_safe_weights_path


def test_accepts_safetensors():
    assert_safe_weights_path("model.safetensors")


def test_accepts_gguf():
    assert_safe_weights_path("model.Q4_K_M.gguf")


def test_rejects_pickle_capable_pt_file():
    with pytest.raises(UnsafeWeightsFormatError):
        assert_safe_weights_path("model.pt")


def test_rejects_pickle_capable_bin_file():
    with pytest.raises(UnsafeWeightsFormatError):
        assert_safe_weights_path("pytorch_model.bin")
