#!/usr/bin/env python3
"""Offline native rendering and pinned AnkiConnect patch safety tests."""

from __future__ import annotations

import hashlib
import importlib.util
import pathlib
import subprocess
import sys
import tempfile
import types
import unittest
from dataclasses import dataclass, field
from html.parser import HTMLParser
from typing import cast
from unittest.mock import patch
from urllib.parse import quote

BASE = pathlib.Path(__file__).resolve().parent


def load(name: str, filename: str) -> types.ModuleType:
    spec = importlib.util.spec_from_file_location(name, BASE / filename)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


NATIVE = load("native_audio", "native_audio.py")
PATCHER = load("patch_ankiconnect", "patch_ankiconnect.py")


@dataclass
class Sound:
    filename: object


@dataclass
class Speech:
    field_text: str = "synthetic TTS"


@dataclass
class Output:
    question: str
    answer: str = ""
    question_av_tags: list[object] = field(default_factory=list)
    answer_av_tags: list[object] = field(default_factory=list)
    question_calls: int = 0
    answer_calls: int = 0

    def question_and_style(self) -> str:
        self.question_calls += 1
        return self.question

    def answer_and_style(self) -> str:
        self.answer_calls += 1
        return self.answer


@dataclass
class Card:
    output: Output
    css: str = ".card { color: navy }"
    id: int = 42
    calls: int = 0

    def render_output(self) -> Output:
        self.calls += 1
        if self.calls != 1:
            raise AssertionError("render output fetched more than once")
        return self.output

    def note_type(self) -> dict[str, object]:
        return {"css": self.css}

    def note(self) -> None:
        raise AssertionError("note fields must never reconstruct native AV order")

    def question(self) -> None:
        raise AssertionError("question must come from the single native output")

    def answer(self) -> None:
        raise AssertionError("answer must come from the single native output")


class MediaElements(HTMLParser):
    def __init__(self, raw: str) -> None:
        super().__init__()
        self.media: list[tuple[str, dict[str, str | None]]] = []
        self.feed(raw)

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        if tag in {"audio", "video"}:
            self.media.append((tag, dict(attrs)))


def render(output: Output, css: str = "") -> dict[str, str]:
    return cast(dict[str, str], NATIVE.render_card(Card(output, css=css)))


class NativeAudioTests(unittest.TestCase):
    def test_one_output_preserves_api_shape_and_side_separation(self) -> None:
        output = Output(
            "<b>Question</b>[anki:play:q:0][anki:play:a:0]",
            "[anki:play:q:0]<hr>[anki:play:a:0]",
            [Sound("front.wav")],
            [Sound("secret-back.mp3")],
        )
        card = Card(output)
        result = NATIVE.render_card(card)
        self.assertEqual(set(result), {"cardId", "questionHtml", "answerHtml", "css"})
        self.assertEqual(result["cardId"], "42")
        self.assertEqual(result["css"], card.css)
        self.assertEqual(
            (card.calls, output.question_calls, output.answer_calls), (1, 1, 1)
        )
        question, answer = result["questionHtml"], result["answerHtml"]
        self.assertEqual(
            [attrs["src"] for _, attrs in MediaElements(question).media], ["front.wav"]
        )
        self.assertNotIn("secret-back", question)
        self.assertIn("Audio unavailable", question)
        self.assertEqual(
            [attrs["src"] for _, attrs in MediaElements(answer).media],
            ["front.wav", "secret-back.mp3"],
        )

    def test_native_conditional_order_and_tts_indices_are_authoritative(self) -> None:
        output = Output(
            "[anki:play:q:2][anki:play:q:0][anki:play:q:1][anki:play:q:3]",
            "[anki:play:a:0]",
            [Sound("reordered.mp3"), Speech(), Sound("visible.wav"), object()],
            [Sound("answer-only.wav")],
        )
        question = render(output)["questionHtml"]
        self.assertEqual(
            [attrs["src"] for _, attrs in MediaElements(question).media],
            ["visible.wav", "reordered.mp3"],
        )
        self.assertEqual(question.count("Audio unavailable"), 2)
        self.assertNotIn("answer-only", question)

    def test_replaces_only_text_without_changing_markup_or_entities(self) -> None:
        prefix = '<DIV data-sound="[anki:play:q:0]">&amp;&#91;keep<!-- [anki:play:q:0] --><style>.x{content:"[anki:play:q:0]"}</style><SCRIPT>var x="[anki:play:q:0]";</SCRIPT>'
        suffix = "</DIV>"
        raw = prefix + "[anki:play:q:0]" + suffix
        result = render(Output(raw, question_av_tags=[Sound("front.wav")]))[
            "questionHtml"
        ]
        self.assertTrue(result.startswith(prefix))
        self.assertTrue(result.endswith(suffix))
        self.assertEqual(len(MediaElements(result).media), 1)

    def test_unknown_malformed_and_forged_markers_are_passive(self) -> None:
        markers = [
            "[anki:play:a:0]",
            "[anki:play:x:0]",
            "[anki:play:q:-1]",
            "[anki:play:q:1]",
            "[anki:play:q:word]",
            "[anki:play:q:0:extra]",
            "[anki:play]",
            "[anki:play:q:0",
            "[anki:play:q:" + "9" * 5000 + "]",
        ]
        for marker in markers:
            with self.subTest(marker=marker[:40]):
                result = render(
                    Output(
                        marker,
                        question_av_tags=[Sound("front.wav")],
                        answer_av_tags=[Sound("secret.wav")],
                    )
                )["questionHtml"]
                self.assertEqual(result, "Audio unavailable")

    def test_safe_basename_url_and_html_escaping_and_video(self) -> None:
        name = "space ü \"quote' & < > ? # %.mp3"
        result = render(Output("[anki:play:q:0]", question_av_tags=[Sound(name)]))[
            "questionHtml"
        ]
        media = MediaElements(result).media
        self.assertEqual(
            media,
            [
                (
                    "audio",
                    {"controls": None, "preload": "none", "src": quote(name, safe="")},
                )
            ],
        )
        self.assertNotIn(name, result)
        for suffix in ["mp4", "webm", "ogv", "m4v", "mov", "mkv", "avi", "MP4"]:
            with self.subTest(suffix=suffix):
                result = render(
                    Output(
                        "[anki:play:q:0]", question_av_tags=[Sound("clip." + suffix)]
                    )
                )["questionHtml"]
                self.assertEqual(MediaElements(result).media[0][0], "video")

    def test_invalid_and_nonportable_filenames_are_unavailable(self) -> None:
        names: list[object] = [
            None,
            42,
            "",
            ".",
            "..",
            "../x.wav",
            "/x.wav",
            "x\\y.wav",
            "http:x.wav",
            "x\x00.wav",
            "x\x7f.wav",
            "x\x85.wav",
            "x\n.wav",
            "x" * 256,
            "ü" * 128,
            "\ud800.wav",
        ]
        for name in names:
            with self.subTest(name=repr(name)):
                result = render(
                    Output("[anki:play:q:0]", question_av_tags=[Sound(name)])
                )["questionHtml"]
                self.assertEqual(result, "Audio unavailable")

    def test_original_and_rewritten_html_css_and_av_bounds_fail_closed(self) -> None:
        cases = [
            (Output("x" * (256 * 1024 + 1)), ""),
            (Output("ü" * (128 * 1024), "x"), ""),
            (Output("x" * (128 * 1024), "y" * (128 * 1024 + 1)), ""),
            (Output(""), "x" * (64 * 1024 + 1)),
            (Output("", question_av_tags=[Speech()] * 4097), ""),
            (Output("", answer_av_tags=[Speech()] * 4097), ""),
            (
                Output(
                    "[anki:play:q:0]" * 1000,
                    question_av_tags=[Sound("ü" * 120 + ".wav")],
                ),
                "",
            ),
        ]
        for index, (output, css) in enumerate(cases):
            with (
                self.subTest(case=index),
                self.assertRaisesRegex(
                    ValueError, "^Card rendering exceeds size limits$"
                ),
            ):
                render(output, css)
        self.assertEqual(
            render(Output("x" * (256 * 1024)), "x" * (64 * 1024))["questionHtml"],
            "x" * (256 * 1024),
        )
        self.assertEqual(
            render(
                Output(
                    "[anki:play:q:4095]",
                    question_av_tags=[Speech()] * 4095 + [Sound("last.wav")],
                )
            )["questionHtml"].count("last.wav"),
            1,
        )


IMPORT = "from .protocol import ApiError, error_record, timestamp\n"
BODY = "    def cards_render(self, id):\n        card = self.card(id)\n        return {'cardId': id, 'questionHtml': card.question(), 'answerHtml': card.answer(), 'css': card.note_type()['css']}\n"
SOURCE = (
    IMPORT
    + "\nclass Services:\n"
    + BODY
    + "\n    def untouched(self):\n        return 17\n"
)


def digest(source: str) -> str:
    return hashlib.sha256(source.encode()).hexdigest()


class PatchSafetyTests(unittest.TestCase):
    def test_fixture_patch_is_exact_and_idempotent(self) -> None:
        result = PATCHER.patch_source(SOURCE, expected_sha256=digest(SOURCE))
        expected = SOURCE.replace(
            IMPORT, IMPORT + "from .native_audio import render_card\n"
        ).replace(
            BODY,
            "    def cards_render(self, id):\n        return render_card(self.card(id))\n",
        )
        self.assertEqual(result, expected)
        self.assertEqual(
            PATCHER.patch_source(result, expected_sha256=digest(SOURCE)), result
        )
        self.assertEqual(
            PATCHER.PINNED_SERVICES_SHA256,
            "3c1132b842f11c30f3fc20f49ea9ba49c67921d8934aac43fd49c1e058d27fd7",
        )

    def test_hash_and_unique_anchors_are_mandatory(self) -> None:
        with self.assertRaises(ValueError):
            PATCHER.patch_source(SOURCE)
        for source in [
            SOURCE + IMPORT,
            SOURCE + BODY,
            SOURCE.replace(IMPORT, ""),
            SOURCE.replace(BODY, ""),
            SOURCE.replace("return 17", "return 18"),
        ]:
            with self.subTest(source_hash=digest(source)):
                expected = (
                    digest(source)
                    if source != SOURCE.replace("return 17", "return 18")
                    else digest(SOURCE)
                )
                with self.assertRaises(ValueError):
                    PATCHER.patch_source(source, expected_sha256=expected)

    def test_cli_refuses_wrong_source_without_writes(self) -> None:
        with tempfile.TemporaryDirectory(prefix="native-audio-patch-") as directory:
            plugin = pathlib.Path(directory)
            services = plugin / "services.py"
            services.write_text(SOURCE)
            result = subprocess.run(
                [sys.executable, "-I", str(BASE / "patch_ankiconnect.py"), str(plugin)],
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(services.read_text(), SOURCE)
            self.assertFalse((plugin / "native_audio.py").exists())

    def test_installs_module_before_patched_source_and_rejects_symlinks(self) -> None:
        with tempfile.TemporaryDirectory(prefix="native-audio-install-") as directory:
            plugin = pathlib.Path(directory)
            services = plugin / "services.py"
            services.write_text(SOURCE)
            pure_patch = PATCHER.patch_source

            def fixture_patch(source: str) -> str:
                return cast(str, pure_patch(source, expected_sha256=digest(SOURCE)))

            atomic_write = PATCHER.atomic_write

            def publish(path: pathlib.Path, raw: bytes, mode: int) -> None:
                if path.name == "services.py":
                    self.assertEqual(
                        (plugin / "native_audio.py").read_bytes(),
                        (BASE / "native_audio.py").read_bytes(),
                    )
                atomic_write(path, raw, mode)

            with (
                patch.object(PATCHER, "patch_source", side_effect=fixture_patch),
                patch.object(PATCHER, "atomic_write", side_effect=publish),
            ):
                PATCHER.patch_plugin(plugin)
                PATCHER.patch_plugin(plugin)
            self.assertEqual(
                (plugin / "native_audio.py").read_bytes(),
                (BASE / "native_audio.py").read_bytes(),
            )
            self.assertEqual(services.read_text(), fixture_patch(SOURCE))
            services.unlink()
            original = plugin / "original.py"
            original.write_text(SOURCE)
            services.symlink_to(original)
            with self.assertRaises(ValueError):
                PATCHER.patch_plugin(plugin)
            self.assertEqual(original.read_text(), SOURCE)


if __name__ == "__main__":
    unittest.main()
