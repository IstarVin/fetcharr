package com.istarvin.fetcharr.ui.history

import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.dto.InfoDto
import com.istarvin.fetcharr.data.network.dto.ReplacementSourceDto
import com.istarvin.fetcharr.data.network.dto.RetryRequest
import com.istarvin.fetcharr.ui.add.HeaderRow
import com.istarvin.fetcharr.ui.add.SubtitleRow
import kotlinx.serialization.encodeToString
import org.junit.Assert.*
import org.junit.Test

class SourceDraftTest {
    private val draft = SourceDraft("id", "Movie", "https://example.com/fresh")

    @Test fun defaultsAreFreshWithNoSavedCredentials() {
        assertTrue(draft.canSubmit)
        assertEquals("clear", draft.headersMode)
        assertTrue(draft.headers.isEmpty())
        assertFalse(draft.copy(submitting = true).canSubmit)
    }

    @Test fun validatesSourcesHeadersAndSubtitles() {
        for (url in listOf("", "file:///tmp/video", "https://", "https://example.com/a b")) assertFalse(draft.copy(url = url).canSubmit)
        assertFalse(draft.copy(subtitles = listOf(SubtitleRow(1, "bad"))).canSubmit)
        assertTrue(draft.copy(subtitles = listOf(SubtitleRow(1))).canSubmit)
        for (headers in listOf(listOf(HeaderRow(1,"Bad:Name","x")), listOf(HeaderRow(1,"Cookie","x\ny")), listOf(HeaderRow(1,"Cookie","x"),HeaderRow(2,"cookie","y")))) {
            assertFalse(draft.copy(headersMode = "replace", headers = headers).canSubmit)
        }
        assertTrue(draft.copy(headersMode = "replace", headers = listOf(HeaderRow(1,"Referer","https://example.com"))).canSubmit)
    }

    @Test fun wireContractDoesNotSendNullHeadersAndOldServersDefaultToUnsupported() {
        val request = FetcharrJson.decodeFromString<RetryRequest>(com.istarvin.fetcharr.data.network.Fixtures.text("retry-source.json"))
        val wire = FetcharrJson.encodeToString(request)
        assertTrue(wire.contains("\"headers_mode\":\"clear\""))
        assertTrue(wire.contains("\"subtitles\":[]"))
        assertFalse(wire.contains("\"headers\":"))
        assertFalse(FetcharrJson.decodeFromString<InfoDto>("""{"version":"old"}""").sourceReplacementSupported)
    }
}
