package com.istarvin.fetcharr.incoming

import java.io.File
import java.util.Base64
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class IntentPayloadParserTest {
    private val parser = IntentPayloadParser()

    private fun fixture(name: String): String =
        javaClass.classLoader!!.getResourceAsStream("intent/$name.json")!!.use { String(it.readBytes(), Charsets.UTF_8) }

    private fun fixtureNames(prefix: String): List<String> {
        val dir = File(javaClass.classLoader!!.getResource("intent")!!.toURI())
        return dir.listFiles()!!.map { it.name }.filter { it.startsWith(prefix) && it.endsWith(".json") }
            .map { it.removeSuffix(".json") }.sorted()
    }

    private fun success(result: ParseResult): IncomingRequest {
        if (result !is ParseResult.Success) throw AssertionError("expected success but was $result")
        return result.request
    }

    private fun failure(result: ParseResult): String {
        if (result !is ParseResult.Failure) throw AssertionError("expected failure but was $result")
        return result.reason
    }

    private fun encodings(json: String): Map<String, String> {
        val bytes = json.toByteArray(Charsets.UTF_8)
        return mapOf(
            "standard-padded" to Base64.getEncoder().encodeToString(bytes),
            "standard-unpadded" to Base64.getEncoder().withoutPadding().encodeToString(bytes),
            "url-safe-padded" to Base64.getUrlEncoder().encodeToString(bytes),
            "url-safe-unpadded" to Base64.getUrlEncoder().withoutPadding().encodeToString(bytes),
        )
    }

    private fun percent(s: String) = s.map { c ->
        if (c.isLetterOrDigit()) c.toString() else "%%%02X".format(c.code)
    }.joinToString("")

    /** Parses [name] via parseJson and via parseUri with every encoding; all must yield the same request. */
    private fun parseAll(name: String): IncomingRequest {
        val json = fixture(name)
        val expected = success(parser.parseJson(json))
        for ((label, b64) in encodings(json)) {
            assertEquals("payload $name/$label", expected, success(parser.parsePayload(b64)))
            assertEquals("uri $name/$label", expected, success(parser.parseUri("fetcharr://download?payload=$b64")))
            assertEquals(
                "uri-pct $name/$label",
                expected,
                success(parser.parseUri("fetcharr://download?x=1&payload=${percent(b64)}#frag")),
            )
        }
        return expected
    }

    @Test
    fun allValidFixturesAreDiscovered() {
        assertEquals(8, fixtureNames("valid-").size)
        assertEquals(19, fixtureNames("invalid-").size)
    }

    @Test
    fun everyValidFixtureParsesInAllEncodings() {
        for (name in fixtureNames("valid-")) parseAll(name)
    }

    @Test
    fun movie() {
        val r = parseAll("valid-movie") as IncomingRequest.Single
        assertEquals("movies", r.categoryHint)
        assertEquals("Example Movie", r.title)
        assertEquals(2026, r.year)
        assertTrue(!r.isEpisodic)
        assertNull(r.showName)
    }

    @Test
    fun minimal() {
        val r = parseAll("valid-single-minimal") as IncomingRequest.Single
        assertNull(r.categoryHint)
        assertTrue(r.title!!.isNotBlank())
        assertTrue(r.headers.isEmpty())
        assertTrue(r.subtitles.isEmpty())
    }

    @Test
    fun episodeWithHeadersAndSubtitles() {
        val r = parseAll("valid-tv-episode-headers-subtitles") as IncomingRequest.Single
        assertEquals("tv", r.categoryHint)
        assertEquals("Pilot", r.title)
        assertEquals("Example Show", r.showName)
        assertEquals(1, r.season)
        assertEquals(1, r.episode)
        assertEquals(2026, r.year)
        assertTrue(r.isEpisodic)
        assertEquals(mapOf("Referer" to "https://stream.example/", "User-Agent" to "CloudStream"), r.headers)
        assertEquals(
            listOf(
                IncomingSubtitle("https://cdn.example/show/s01e01.en.vtt", "en"),
                IncomingSubtitle("https://cdn.example/show/s01e01.es.vtt", "es"),
            ),
            r.subtitles,
        )
    }

    @Test
    fun episodeWithoutTitleIsAllowedByD6() {
        val r = parseAll("valid-single-episode-no-title") as IncomingRequest.Single
        assertNull(r.title)
        assertTrue(r.isEpisodic)
        assertEquals(2, r.episode)
    }

    @Test
    fun specialsZeroAllowed() {
        val r = parseAll("valid-single-episode-specials-zero") as IncomingRequest.Single
        assertEquals(0, r.season)
        assertEquals(0, r.episode)
        assertTrue(r.isEpisodic)
    }

    @Test
    fun batchSeason() {
        val r = parseAll("valid-batch-season") as IncomingRequest.Batch
        assertEquals("tv", r.categoryHint)
        assertEquals("Example Show", r.showName)
        assertEquals(1, r.season)
        assertEquals(2026, r.year)
        assertEquals(mapOf("Referer" to "https://stream.example/"), r.headers)
        assertEquals(listOf(1, 2, 3), r.episodes.map { it.episode })
        assertEquals(listOf("Pilot", null, "Third Episode"), r.episodes.map { it.title })
        assertEquals(1, r.episodes[0].subtitles.size)
        assertTrue(r.isEpisodic)
    }

    @Test
    fun batchNoCategory() {
        val r = parseAll("valid-batch-no-category") as IncomingRequest.Batch
        assertNull(r.categoryHint)
        assertNull(r.year)
        assertTrue(r.episodes.isNotEmpty())
    }

    @Test
    fun unknownKeysIgnored() {
        val r = parseAll("valid-unknown-keys-ignored") as IncomingRequest.Single
        assertEquals("Example Movie", r.title)
        assertEquals("movies", r.categoryHint)
    }

    // ---- invalid fixtures ---------------------------------------------------------------------

    private val expectedReasonKeyword = mapOf(
        "invalid-neither-single-nor-batch" to "neither",
        "invalid-both-single-and-batch" to "both",
        "invalid-single-missing-url" to "single.url missing",
        "invalid-single-url-not-http" to "single.url is not an http/https",
        "invalid-single-url-no-scheme" to "single.url is not an http/https",
        "invalid-single-missing-title" to "single.title missing",
        "invalid-single-no-title-partial-episode" to "single.title missing",
        "invalid-single-negative-season" to "single.season is negative",
        "invalid-single-negative-episode" to "single.episode is negative",
        "invalid-single-subtitle-url-not-http" to "subtitles[0].url is not an http/https",
        "invalid-single-subtitle-missing-url" to "subtitles[0].url missing",
        "invalid-batch-missing-showName" to "batch.showName missing",
        "invalid-batch-missing-season" to "batch.season missing",
        "invalid-batch-missing-episodes" to "batch.episodes missing",
        "invalid-batch-empty-episodes" to "batch.episodes is empty",
        "invalid-batch-episode-missing-episode" to "episodes[0].episode missing",
        "invalid-batch-episode-missing-url" to "episodes[0].url missing",
        "invalid-batch-episode-url-not-http" to "episodes[0].url is not an http/https",
        "invalid-batch-episode-subtitle-url-not-http" to "subtitles[0].url is not an http/https",
    )

    @Test
    fun everyInvalidFixtureIsRejectedWithItsReason() {
        val names = fixtureNames("invalid-")
        assertEquals(expectedReasonKeyword.keys, names.toSet())
        for (name in names) {
            val json = fixture(name)
            val keyword = expectedReasonKeyword.getValue(name)
            val viaJson = failure(parser.parseJson(json))
            assertTrue("$name: '$viaJson' should contain '$keyword'", viaJson.contains(keyword))
            for ((label, b64) in encodings(json)) {
                assertEquals("$name/$label payload", viaJson, failure(parser.parsePayload(b64)))
                assertEquals("$name/$label uri", viaJson, failure(parser.parseUri("fetcharr://download?payload=$b64")))
            }
        }
    }

    // ---- raw input rejections -----------------------------------------------------------------

    private val validB64 = encodings(fixture("valid-movie")).getValue("standard-padded")

    @Test
    fun wrongSchemeRejected() {
        assertTrue(failure(parser.parseUri("https://download?payload=$validB64")).contains("scheme"))
        assertTrue(failure(parser.parseUri("fetcharr2://download?payload=$validB64")).contains("scheme"))
        assertTrue(failure(parser.parseUri("")).contains("scheme"))
        assertTrue(failure(parser.parseUri("no scheme at all")).contains("scheme"))
    }

    @Test
    fun anyFetcharrHostAndPathAccepted() {
        success(parser.parseUri("fetcharr://anything/else?payload=$validB64"))
        success(parser.parseUri("FETCHARR://download?payload=$validB64"))
        success(parser.parseUri("fetcharr:///?payload=$validB64"))
    }

    @Test
    fun missingPayloadParamRejected() {
        for (uri in listOf("fetcharr://download", "fetcharr://download?", "fetcharr://download?other=1", "fetcharr://download?payload=")) {
            assertTrue(uri, failure(parser.parseUri(uri)).contains("missing payload"))
        }
    }

    @Test
    fun payloadNotBase64Rejected() {
        assertTrue(failure(parser.parsePayload("!!!not base64!!!")).contains("Base64"))
        assertTrue(failure(parser.parsePayload("")).contains("Base64"))
        assertTrue(failure(parser.parsePayload("A")).contains("Base64"))
        assertTrue(failure(parser.parseUri("fetcharr://download?payload=%$$")).contains("percent"))
    }

    @Test
    fun decodedTextNotJsonRejected() {
        val b64 = Base64.getEncoder().encodeToString("this is not json".toByteArray())
        assertTrue(failure(parser.parsePayload(b64)).contains("JSON"))
        assertTrue(failure(parser.parseJson("{\"single\":")).contains("JSON"))
        assertTrue(failure(parser.parseJson("[1,2]")).contains("not an object"))
    }

    @Test
    fun wrongFieldTypesRejected() {
        val bad = listOf(
            """{"single":{"url":"https://a.example/x","title":"T","season":"1"}}""",
            """{"single":{"url":"https://a.example/x","title":"T","year":1.5}}""",
            """{"single":{"url":"https://a.example/x","title":5}}""",
            """{"headers":{"a":1},"single":{"url":"https://a.example/x","title":"T"}}""",
            """{"single":"x"}""",
            """{"single":{"url":"https://","title":"T"}}""",
        )
        for (json in bad) failure(parser.parseJson(json))
    }

    @Test
    fun urlSafeAlphabetWithSpecialCharactersRoundTrips() {
        // '>' and '?' sequences force '+' and '/' in standard Base64 and '-' and '_' in URL-safe Base64.
        val json = """{"single":{"url":"https://a.example/x?a=b","title":"???>>>~~~ é 日本"}}"""
        val enc = encodings(json)
        assertTrue(enc.getValue("standard-padded").let { '+' in it || '/' in it })
        assertTrue(enc.getValue("url-safe-padded").let { '-' in it || '_' in it })
        for ((label, b64) in enc) {
            val r = success(parser.parseUri("fetcharr://download?payload=$b64")) as IncomingRequest.Single
            assertEquals(label, "???>>>~~~ é 日本", r.title)
            val r2 = success(parser.parseUri("fetcharr://download?payload=${percent(b64)}")) as IncomingRequest.Single
            assertEquals(label, "???>>>~~~ é 日本", r2.title)
        }
    }

    @Test
    fun failureReasonNeverEchoesPayloadContent() {
        val reason = failure(parser.parseJson("""{"single":{"url":"ftp://secret-host.example/x","title":"T"}}"""))
        assertTrue(!reason.contains("secret-host"))
    }

    @Test
    fun batchDuplicateEpisodeLastWinsInNormalization() {
        val json = """{"headers":{"A":"root","B":"root"},"batch":{"showName":"S","season":1,"episodes":[
            {"episode":2,"url":"https://a.example/2a"},
            {"episode":1,"url":"https://a.example/1","headers":{"B":"ep"}},
            {"episode":2,"url":"https://a.example/2b"}]}}"""
        val r = success(parser.parseJson(json))
        val entries = r.toEpisodeEntries()
        assertEquals(listOf(2, 1), entries.map { it.episode })
        assertEquals("https://a.example/2b", entries[0].url)
        assertEquals(mapOf("A" to "root", "B" to "ep"), entries[1].headers)
        assertEquals(mapOf("A" to "root", "B" to "root"), entries[0].headers)
    }

    @Test
    fun normalization() {
        val episode = success(parser.parseJson(fixture("valid-tv-episode-headers-subtitles")))
        assertEquals(1, episode.toEpisodeEntries().size)
        assertEquals(episode.headers, episode.toEpisodeEntries().single().headers)
        assertEquals(SeasonKey.of("example show ", 1), episode.seasonKey())
        val movie = success(parser.parseJson(fixture("valid-movie")))
        assertTrue(movie.toEpisodeEntries().isEmpty())
        assertNull(movie.seasonKey())
        val batch = success(parser.parseJson(fixture("valid-batch-season")))
        assertEquals(3, batch.toEpisodeEntries().size)
        assertEquals(mapOf("Referer" to "https://stream.example/"), batch.toEpisodeEntries()[1].headers)
    }
}
