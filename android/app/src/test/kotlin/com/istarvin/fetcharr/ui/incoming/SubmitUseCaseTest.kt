package com.istarvin.fetcharr.ui.incoming

import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.Fixtures
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.ServerConfigProvider
import com.istarvin.fetcharr.data.network.dto.SkippedEpisodeDto
import com.istarvin.fetcharr.data.network.dto.SubtitleDto
import com.istarvin.fetcharr.incoming.IncomingSubtitle
import com.istarvin.fetcharr.incoming.SeasonEpisode
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.TimeUnit

class SubmitUseCaseTest {
    private lateinit var server: MockWebServer
    private val config = MutableStateFlow<ServerConfig?>(null)
    private lateinit var useCase: SubmitUseCase

    @Before
    fun setUp() {
        server = MockWebServer().also { it.start() }
        val client = OkHttpClient.Builder()
            .connectTimeout(3, TimeUnit.SECONDS)
            .readTimeout(3, TimeUnit.SECONDS)
            .build()
        val provider = object : ServerConfigProvider {
            override val config = this@SubmitUseCaseTest.config
        }
        useCase = SubmitUseCase(FetcharrApiProvider(FetcharrApiFactory(client, FetcharrJson), provider))
        config.value = ServerConfig(server.url("/").toString().trimEnd('/'), "tok")
    }

    @After
    fun tearDown() {
        runCatching { server.shutdown() }
    }

    private fun json(code: Int, body: String) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    private fun parse(text: String) = Json.parseToJsonElement(text)

    private val referer = mapOf("Referer" to "https://example.com/")

    // ---- single ----------------------------------------------------------------------------

    @Test
    fun tvEpisodeBodyMatchesContractFixture() {
        server.enqueue(json(201, Fixtures.text("job-pending.json")))
        val form = ItemForm(show = "Andor", season = "1", episode = "3", year = "2022")
        val outcome = runBlocking {
            useCase.submitJob("https://cdn.example/andor/s01e03.m3u8", "tv", "tv", form, referer, emptyList())
        }
        assertEquals(SubmitOutcome.Success("Added 1 download"), outcome)
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertEquals("/api/jobs", req.path)
        assertEquals("Bearer tok", req.getHeader("Authorization"))
        assertEquals(parse(Fixtures.text("create-job-tv.json")), parse(req.body.readUtf8()))
    }

    @Test
    fun movieBodyMatchesContractFixture() {
        server.enqueue(json(201, Fixtures.text("job-pending.json")))
        val form = ItemForm(title = "Dune Part Two", year = "2024", show = "ignored", season = "9")
        val outcome = runBlocking {
            useCase.submitJob(
                "https://cdn.example/dune2.m3u8", "movies", "movie", form, referer,
                listOf(SubtitleDto("https://cdn.example/dune2.en.vtt", "en")),
            )
        }
        assertTrue(outcome is SubmitOutcome.Success)
        assertEquals(parse(Fixtures.text("create-job-movie.json")), parse(server.takeRequest().body.readUtf8()))
    }

    @Test
    fun flatSendsOnlyTitleAndOmitsEmptyHeadersAndSubtitles() {
        server.enqueue(json(201, Fixtures.text("job-pending.json")))
        runBlocking {
            useCase.submitJob("https://cdn.example/f.mp4", "misc", "flat", ItemForm(title = " Clip ", year = "2020"), emptyMap(), emptyList())
        }
        val body = parse(server.takeRequest().body.readUtf8()).jsonObject
        assertEquals(setOf("url", "category", "title"), body.keys)
        assertEquals("Clip", body["title"]!!.jsonPrimitive.content)
    }

    @Test
    fun tvEpisodeTitleIsSentAsTitle() {
        server.enqueue(json(201, Fixtures.text("job-pending.json")))
        runBlocking {
            useCase.submitJob(
                "https://cdn.example/e.m3u8", "tv", "tv",
                ItemForm(show = "Andor", season = "1", episode = "3", episodeTitle = "Narkina 5"), emptyMap(), emptyList(),
            )
        }
        assertEquals("Narkina 5", parse(server.takeRequest().body.readUtf8()).jsonObject["title"]!!.jsonPrimitive.content)
    }

    @Test
    fun duplicateSingle() {
        server.enqueue(json(409, Fixtures.text("error-duplicate.json")))
        val outcome = runBlocking { useCase.submitJob("https://cdn.example/dune2.m3u8", "movies", "movie", ItemForm(title = "D"), emptyMap(), emptyList()) }
        assertEquals(SubmitOutcome.DuplicateSingle, outcome)
    }

    @Test
    fun alreadyExistsAsksThenOverwriteIsSent() {
        server.enqueue(json(409, Fixtures.text("error-already_exists.json")))
        server.enqueue(json(201, Fixtures.text("job-pending.json")))
        val outcome = runBlocking { useCase.submitJob("https://cdn.example/dune2.m3u8", "movies", "movie", ItemForm(title = "D"), emptyMap(), emptyList()) }
        assertEquals(SubmitOutcome.AlreadyExists, outcome)
        assertFalse(parse(server.takeRequest().body.readUtf8()).jsonObject.containsKey("overwrite"))

        val replaced = runBlocking {
            useCase.submitJob("https://cdn.example/dune2.m3u8", "movies", "movie", ItemForm(title = "D"), emptyMap(), emptyList(), overwrite = true)
        }
        assertEquals(SubmitOutcome.Success("Added 1 download"), replaced)
        assertEquals("true", parse(server.takeRequest().body.readUtf8()).jsonObject["overwrite"]!!.jsonPrimitive.content)
    }

    @Test
    fun batchMessageCountsSkipReasons() {
        val skipped = listOf(
            SkippedEpisodeDto(1, "already_exists"), SkippedEpisodeDto(2, "already_exists"), SkippedEpisodeDto(3, "duplicate"),
        )
        assertEquals("Added 4 · 2 already in library · 1 already queued", SubmitUseCase.batchMessage(4, skipped))
        assertEquals("Added 4 · 2 already in library", SubmitUseCase.batchMessage(4, skipped.take(2)))
        assertEquals("Added 4 downloads", SubmitUseCase.batchMessage(4, emptyList()))
    }

    @Test
    fun batchAllInLibraryAsksAndOverwriteIsSent() {
        server.enqueue(json(409, Fixtures.text("error-already_exists.json")))
        server.enqueue(json(201, Fixtures.text("batch-create-response.json")))
        val form = ItemForm(show = "S", season = "2")
        assertEquals(SubmitOutcome.AlreadyExists, runBlocking { useCase.submitSeason("tv", form, listOf(ep(1), ep(2))) })
        assertFalse(parse(server.takeRequest().body.readUtf8()).jsonObject.containsKey("overwrite"))
        runBlocking { useCase.submitSeason("tv", form, listOf(ep(1), ep(2)), overwrite = true) }
        assertEquals("true", parse(server.takeRequest().body.readUtf8()).jsonObject["overwrite"]!!.jsonPrimitive.content)
    }

    @Test
    fun badRequestShowsServerMessage() {
        server.enqueue(json(400, Fixtures.text("error-invalid_request.json")))
        val outcome = runBlocking { useCase.submitJob("https://cdn.example/a", "foo", "flat", ItemForm(title = "D"), emptyMap(), emptyList()) }
        assertEquals(SubmitOutcome.Error("category 'foo' does not exist"), outcome)
    }

    @Test
    fun unauthorized() {
        server.enqueue(json(401, Fixtures.text("error-unauthorized.json")))
        val outcome = runBlocking { useCase.submitJob("https://cdn.example/a", "c", "flat", ItemForm(title = "D"), emptyMap(), emptyList()) }
        assertEquals(SubmitOutcome.Error("Wrong API token"), outcome)
    }

    @Test
    fun serverError() {
        server.enqueue(json(500, """{"error":{"code":"internal","message":"boom"}}"""))
        val outcome = runBlocking { useCase.submitJob("https://cdn.example/a", "c", "flat", ItemForm(title = "D"), emptyMap(), emptyList()) }
        assertEquals(SubmitOutcome.Error("Server error — try again"), outcome)
    }

    @Test
    fun unreachable() {
        server.shutdown()
        val outcome = runBlocking { useCase.submitJob("https://cdn.example/a", "c", "flat", ItemForm(title = "D"), emptyMap(), emptyList()) }
        assertEquals(SubmitOutcome.Error("Can't reach server"), outcome)
    }

    @Test
    fun notConfigured() {
        config.value = null
        val single = runBlocking { useCase.submitJob("https://cdn.example/a", "c", "flat", ItemForm(title = "D"), emptyMap(), emptyList()) }
        val season = runBlocking { useCase.submitSeason("tv", ItemForm(show = "S", season = "1"), listOf(ep(1), ep(2))) }
        assertEquals(SubmitOutcome.NotConfigured, single)
        assertEquals(SubmitOutcome.NotConfigured, season)
        assertEquals(0, server.requestCount)
    }

    // ---- season ----------------------------------------------------------------------------

    private fun ep(n: Int, selected: Boolean = true, headers: Map<String, String> = emptyMap()) = SeasonEpisode(
        episode = n, title = "Title $n", url = "https://cdn.example/sev/s02e0$n.m3u8", headers = headers,
        subtitles = if (n == 1) listOf(IncomingSubtitle("https://cdn.example/sev/s02e01.en.vtt", "en")) else emptyList(),
        selected = selected,
    )

    @Test
    fun batchSendsOnlySelectedEpisodesWithTheirOwnHeaders() {
        server.enqueue(json(201, Fixtures.text("batch-create-response.json")))
        val episodes = listOf(ep(1, headers = referer), ep(2, headers = referer + ("Cookie" to "session=abc123")), ep(3, selected = false))
        val outcome = runBlocking { useCase.submitSeason("tv", ItemForm(show = "Severance", season = "2", year = "2022"), episodes) }
        // batch-create-response.json: 2 jobs created, 1 skipped as duplicate
        assertEquals(SubmitOutcome.Success("Added 2 · 1 already queued"), outcome)

        val req = server.takeRequest()
        assertEquals("/api/jobs/batch", req.path)
        val body = parse(req.body.readUtf8()).jsonObject
        assertEquals("tv", body["category"]!!.jsonPrimitive.content)
        assertEquals("Severance", body["show_name"]!!.jsonPrimitive.content)
        assertEquals(2, body["season"]!!.jsonPrimitive.content.toInt())
        assertEquals(2022, body["year"]!!.jsonPrimitive.content.toInt())
        assertFalse(body.containsKey("headers"))
        val sent = body["episodes"]!!.jsonArray
        assertEquals(listOf("1", "2"), sent.map { it.jsonObject["episode"]!!.jsonPrimitive.content })
        assertEquals(referer.keys, sent[0].jsonObject["headers"]!!.jsonObject.keys)
        assertEquals(setOf("Referer", "Cookie"), sent[1].jsonObject["headers"]!!.jsonObject.keys)
        assertEquals("en", sent[0].jsonObject["subtitles"]!!.jsonArray.single().jsonObject["lang"]!!.jsonPrimitive.content)
        assertFalse(sent[1].jsonObject.containsKey("subtitles"))
    }

    @Test
    fun batchWithoutSkippedSaysAddedN() {
        val resp = parse(Fixtures.text("batch-create-response.json")).jsonObject
        val noSkips = JsonObject(resp + ("skipped" to JsonArray(emptyList())))
        server.enqueue(json(201, noSkips.toString()))
        val outcome = runBlocking { useCase.submitSeason("tv", ItemForm(show = "S", season = "2"), listOf(ep(1), ep(2))) }
        assertEquals(SubmitOutcome.Success("Added 2 downloads"), outcome)
    }

    @Test
    fun batchWhereEverythingIsDuplicateClosesLikeSingleDuplicate() {
        server.enqueue(json(409, Fixtures.text("error-duplicate.json")))
        val outcome = runBlocking { useCase.submitSeason("tv", ItemForm(show = "S", season = "2"), listOf(ep(1), ep(2))) }
        assertEquals(SubmitOutcome.DuplicateSingle, outcome)
    }

    @Test
    fun batchBadRequestShowsServerMessage() {
        server.enqueue(json(400, Fixtures.text("error-invalid_request.json")))
        val outcome = runBlocking { useCase.submitSeason("tv", ItemForm(show = "S", season = "2"), listOf(ep(1), ep(2))) }
        assertEquals(SubmitOutcome.Error("category 'foo' does not exist"), outcome)
    }

    @Test
    fun oneSelectedEpisodeOfASeasonUsesPostJobs() {
        server.enqueue(json(201, Fixtures.text("job-pending.json")))
        val outcome = runBlocking {
            useCase.submitSeason("tv", ItemForm(show = "Severance", season = "2", year = "2022"), listOf(ep(1, selected = false), ep(2, headers = referer)))
        }
        assertEquals(SubmitOutcome.Success("Added 1 download"), outcome)
        val req = server.takeRequest()
        assertEquals("/api/jobs", req.path)
        val body = parse(req.body.readUtf8()).jsonObject
        assertEquals("https://cdn.example/sev/s02e02.m3u8", body["url"]!!.jsonPrimitive.content)
        assertEquals("Severance", body["show_name"]!!.jsonPrimitive.content)
        assertEquals("2", body["episode"]!!.jsonPrimitive.content)
        assertEquals("Title 2", body["title"]!!.jsonPrimitive.content)
    }

    @Test
    fun nothingSelectedIsAnErrorWithoutARequest() {
        val outcome = runBlocking { useCase.submitSeason("tv", ItemForm(show = "S", season = "2"), listOf(ep(1, selected = false))) }
        assertTrue(outcome is SubmitOutcome.Error)
        assertEquals(0, server.requestCount)
    }

    @Test
    fun addedMessages() {
        assertEquals("Added 1 download", SubmitUseCase.addedMessage(1))
        assertEquals("Added 6 downloads", SubmitUseCase.addedMessage(6))
    }
}
