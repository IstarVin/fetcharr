package dev.istarvin.fetcharr.notifications.logic

import dev.istarvin.fetcharr.data.network.FetcharrJson
import dev.istarvin.fetcharr.data.network.dto.JobDto
import org.junit.Assert.assertEquals
import org.junit.Test

class JobTitlesTest {
    @Test fun episodeHeading() =
        assertEquals("Severance — S02E03", JobTitles.heading(job("a", showName = "Severance", season = 2, episode = 3)))

    @Test fun twoDigitEpisodeNotPadded() =
        assertEquals("Show — S10E12", JobTitles.heading(job("a", showName = "Show", season = 10, episode = 12)))

    @Test fun movieWithYear() =
        assertEquals("Dune Part Two (2024)", JobTitles.heading(job("a", title = "Dune Part Two", year = 2024)))

    @Test fun movieWithoutYear() = assertEquals("Dune", JobTitles.heading(job("a", title = "Dune")))

    @Test fun showWithoutNumbers() = assertEquals("Severance", JobTitles.heading(job("a", showName = "Severance")))

    @Test fun urlFallback() {
        assertEquals("cdn.example/video.m3u8", JobTitles.heading(job("a", url = "https://cdn.example/path/video.m3u8?x=1")))
        assertEquals("cdn.example", JobTitles.heading(job("a", url = "https://cdn.example/")))
    }

    @Test fun batchHeading() {
        assertEquals("Severance S02", JobTitles.batchHeading("Severance", 2))
        assertEquals("Severance", JobTitles.batchHeading("Severance", null))
        assertEquals("Batch", JobTitles.batchHeading(null, 2))
    }

    @Test fun fixtureHeading() {
        val text = javaClass.classLoader!!.getResourceAsStream("job-failed.json")!!.use { String(it.readBytes()) }
        val dto = FetcharrJson.decodeFromString(JobDto.serializer(), text)
        assertEquals("Andor — S01E03", JobTitles.heading(dto))
    }
}
