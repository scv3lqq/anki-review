"""Resolve native Anki AV markers from the same rendered output as their HTML."""

from __future__ import annotations

import html
import re
import unicodedata
from collections.abc import Mapping, Sequence
from html.parser import HTMLParser
from typing import Protocol, TypedDict
from urllib.parse import quote

MAX_HTML_BYTES = 256 * 1024
MAX_CSS_BYTES = 64 * 1024
MAX_AV_TAGS = 4096
SIZE_ERROR = "Card rendering exceeds size limits"
UNAVAILABLE = "Audio unavailable"
VIDEO_SUFFIXES = frozenset({"mp4", "webm", "ogv", "m4v", "mov", "mkv", "avi"})
MARKER = re.compile(r"\[anki:play(?::[^\]\r\n]*)?(?:\]|(?=[\r\n])|$)")
REFERENCE = re.compile(r"\[anki:play:([qa]):([0-9]{1,4})\]")


class RenderOutput(Protocol):
    @property
    def question_av_tags(self) -> Sequence[object]: ...

    @property
    def answer_av_tags(self) -> Sequence[object]: ...

    def question_and_style(self) -> str: ...

    def answer_and_style(self) -> str: ...


class NativeCard(Protocol):
    @property
    def id(self) -> int: ...

    def render_output(self) -> RenderOutput: ...

    def note_type(self) -> Mapping[str, object]: ...


class RenderedCard(TypedDict):
    cardId: str
    questionHtml: str
    answerHtml: str
    css: str


def media_name(value: object) -> str | None:
    if not isinstance(value, str) or value in {"", ".", ".."}:
        return None
    try:
        encoded = value.encode("utf-8")
    except UnicodeEncodeError:
        return None
    if len(encoded) > 255 or any(
        character in "/\\:" or unicodedata.category(character) == "Cc"
        for character in value
    ):
        return None
    return value


class MarkerRenderer(HTMLParser):
    def __init__(
        self,
        source: str,
        question_tags: Sequence[object],
        answer_tags: Sequence[object],
        *,
        answer_side: bool,
    ) -> None:
        super().__init__(convert_charrefs=False)
        self.source = source
        self.question_tags = question_tags
        self.answer_tags = answer_tags
        self.answer_side = answer_side
        self.raw_tag: str | None = None
        self.edits: list[tuple[int, int, str]] = []
        self.line_offsets = [0]
        for match in re.finditer("\n", source):
            self.line_offsets.append(match.end())

    def handle_starttag(self, tag: str, _attrs: list[tuple[str, str | None]]) -> None:
        if tag in {"script", "style"}:
            self.raw_tag = tag

    def handle_endtag(self, tag: str) -> None:
        if tag == self.raw_tag:
            self.raw_tag = None

    def handle_data(self, data: str) -> None:
        if self.raw_tag is not None:
            return
        replaced = MARKER.sub(self.resolve, data)
        if replaced != data:
            line, column = self.getpos()
            offset = self.line_offsets[line - 1] + column
            self.edits.append((offset, offset + len(data), replaced))

    def resolve(self, marker: re.Match[str]) -> str:
        reference = REFERENCE.fullmatch(marker.group())
        if reference is None:
            return UNAVAILABLE
        side, index_text = reference.groups()
        if side == "a" and not self.answer_side:
            return UNAVAILABLE
        tags = self.question_tags if side == "q" else self.answer_tags
        index = int(index_text)
        if index >= len(tags):
            return UNAVAILABLE
        filename = media_name(getattr(tags[index], "filename", None))
        if filename is None:
            return UNAVAILABLE
        element = (
            "video"
            if filename.rsplit(".", 1)[-1].lower() in VIDEO_SUFFIXES
            else "audio"
        )
        source = html.escape(quote(filename, safe=""), quote=True)
        return f'<{element} controls preload="none" src="{source}"></{element}>'

    def render(self) -> str:
        self.feed(self.source)
        self.close()
        pieces: list[str] = []
        offset = 0
        for start, end, replacement in self.edits:
            pieces.extend((self.source[offset:start], replacement))
            offset = end
        pieces.append(self.source[offset:])
        return "".join(pieces)


def html_size(question: str, answer: str) -> int:
    return len(question.encode("utf-8")) + len(answer.encode("utf-8"))


def render_card(card: NativeCard) -> RenderedCard:
    output = card.render_output()
    question, answer = output.question_and_style(), output.answer_and_style()
    css = card.note_type()["css"]
    if not isinstance(css, str):
        raise ValueError(SIZE_ERROR)  # noqa: TRY004 - keep the approved fail-closed rendering error.
    question_tags, answer_tags = output.question_av_tags, output.answer_av_tags
    if (
        html_size(question, answer) > MAX_HTML_BYTES
        or len(css.encode("utf-8")) > MAX_CSS_BYTES
        or len(question_tags) > MAX_AV_TAGS
        or len(answer_tags) > MAX_AV_TAGS
    ):
        raise ValueError(SIZE_ERROR)
    question = MarkerRenderer(
        question, question_tags, answer_tags, answer_side=False
    ).render()
    answer = MarkerRenderer(
        answer, question_tags, answer_tags, answer_side=True
    ).render()
    if html_size(question, answer) > MAX_HTML_BYTES:
        raise ValueError(SIZE_ERROR)
    return {
        "cardId": str(card.id),
        "questionHtml": question,
        "answerHtml": answer,
        "css": css,
    }
