package dev.istarvin.fetcharr.ui.incoming

import dev.istarvin.fetcharr.incoming.IncomingSubtitle
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SourceLineTest {
    private fun sub(lang: String?) = IncomingSubtitle("https://cdn.example/s.vtt", lang)

    @Test
    fun hostWithSubtitles() {
        assertEquals("cdn.example · 2 subtitles: en, es", SourceLine.of("https://cdn.example/a/b.m3u8", listOf(sub("en"), sub("es"))))
        assertEquals("cdn.example · 1 subtitle: en", SourceLine.of("https://cdn.example:8443/a", listOf(sub("en"))))
        assertEquals("cdn.example · 1 subtitle", SourceLine.of("https://cdn.example/a", listOf(sub(null))))
    }

    @Test
    fun hostOnlyWithoutSubtitles() {
        assertEquals("cdn.example", SourceLine.of("https://cdn.example/a", emptyList()))
    }
}

class ItemFormTest {
    @Test
    fun layoutsRequireTheirFields() {
        assertFalse(ItemForm().isCompleteFor("movie"))
        assertTrue(ItemForm(title = "Dune").isCompleteFor("movie"))
        assertFalse(ItemForm(title = "Dune", year = "20").isCompleteFor("movie"))
        assertTrue(ItemForm(title = "Dune", year = "2024").isCompleteFor("movie"))
        assertTrue(ItemForm(title = "x").isCompleteFor("flat"))
        assertFalse(ItemForm(title = "  ").isCompleteFor("flat"))
        assertFalse(ItemForm(show = "A", season = "1").isCompleteFor("tv"))
        assertTrue(ItemForm(show = "A", season = "0", episode = "0").isCompleteFor("tv"))
        assertTrue(ItemForm(show = "A", season = "1").isCompleteForSeason())
        assertFalse(ItemForm(show = "", season = "1").isCompleteForSeason())
    }

    @Test
    fun httpUrlRules() {
        assertTrue(isHttpUrl("https://a.example/x"))
        assertTrue(isHttpUrl(" HTTP://a.example "))
        assertFalse(isHttpUrl("ftp://a.example"))
        assertFalse(isHttpUrl("https://"))
        assertFalse(isHttpUrl("https://a b.example"))
        assertFalse(isHttpUrl("a.example"))
    }
}
