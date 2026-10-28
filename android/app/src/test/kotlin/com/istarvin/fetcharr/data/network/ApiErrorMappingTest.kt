package com.istarvin.fetcharr.data.network

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ApiErrorMappingTest {
    private val json = FetcharrJson

    private fun map(status: Int, name: String) = ApiErrorMapper.fromHttp(json, status, Fixtures.text("error-$name.json"))

    @Test
    fun fixturesMapByCode() {
        assertEquals(ApiError.BadRequest("invalid_request", "category 'foo' does not exist"), map(400, "invalid_request"))
        assertEquals(ApiError.Unauthorized, map(401, "unauthorized"))
        assertTrue(map(404, "not_found") is ApiError.NotFound)
        assertTrue(map(409, "invalid_state") is ApiError.InvalidState)
        val dup = map(409, "duplicate") as ApiError.Duplicate
        assertTrue(dup.message.contains("already active"))
        val exists = map(409, "already_exists") as ApiError.AlreadyExists
        assertTrue(exists.message.contains("already exists"))
        assertEquals(ApiError.YtdlpUnmanaged, map(409, "ytdlp_unmanaged"))
        assertEquals(ApiError.YtdlpBusy, map(409, "ytdlp_busy"))
    }

    @Test
    fun codeWinsOverStatus() {
        // Even with an unexpected status the code decides.
        assertEquals(ApiError.YtdlpBusy, map(500, "ytdlp_busy"))
        assertEquals(ApiError.Unauthorized, map(403, "unauthorized"))
    }

    @Test
    fun duplicateCarriesDetails() {
        val body = """{"error":{"code":"duplicate","message":"m","details":{"job_id":"abc"}}}"""
        val dup = ApiErrorMapper.fromHttp(json, 409, body) as ApiError.Duplicate
        assertNotNull(dup.details)
        assertTrue(dup.details.toString().contains("abc"))
    }

    @Test
    fun statusFallbacks() {
        assertEquals(ApiError.Server(500), ApiErrorMapper.fromHttp(json, 500, """{"error":{"code":"internal","message":"x"}}"""))
        assertEquals(ApiError.Server(503), ApiErrorMapper.fromHttp(json, 503, "<html>bad gateway</html>"))
        assertEquals(ApiError.Unauthorized, ApiErrorMapper.fromHttp(json, 401, ""))
        assertEquals(
            ApiError.BadRequest("weird", "m"),
            ApiErrorMapper.fromHttp(json, 400, """{"error":{"code":"weird","message":"m"}}"""),
        )
        assertEquals(
            ApiError.Unknown(409, "weird", "m"),
            ApiErrorMapper.fromHttp(json, 409, """{"error":{"code":"weird","message":"m"}}"""),
        )
        assertEquals(ApiError.InvalidResponse, ApiErrorMapper.fromHttp(json, 404, "<html>nope</html>"))
        assertEquals(ApiError.InvalidResponse, ApiErrorMapper.fromHttp(json, 400, null))
    }

    @Test
    fun userMessages() {
        assertEquals("Can't reach server", ApiError.Unreachable.userMessage)
        assertEquals("Wrong API token", ApiError.Unauthorized.userMessage)
        assertEquals("category 'foo' does not exist", map(400, "invalid_request").userMessage)
        assertEquals("Job already changed", map(409, "invalid_state").userMessage)
        assertEquals("Server error — try again", ApiError.Server(500).userMessage)
        assertEquals("Downloads are running — try again when they finish", ApiError.YtdlpBusy.userMessage)
        assertEquals("Already in the queue", map(409, "duplicate").userMessage)
        assertEquals("Already in library", map(409, "already_exists").userMessage)
    }
}
