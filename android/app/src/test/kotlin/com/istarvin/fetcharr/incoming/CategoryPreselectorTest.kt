package com.istarvin.fetcharr.incoming

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class CategoryPreselectorTest {
    private val categories = listOf(
        CategoryOption("flat1", "Misc", "flat"),
        CategoryOption("movies", "Movies", "movie"),
        CategoryOption("movies4k", "Movies 4K", "movie"),
        CategoryOption("shows", "TV Shows", "tv"),
        CategoryOption("anime", "Anime", "tv"),
    )

    private fun movie(hint: String? = null) =
        IncomingRequest.Single(hint, "https://a.example/m.mp4", "M", 2026, null, null, null, emptyMap(), emptyList())

    private fun episode(hint: String? = null) =
        IncomingRequest.Single(hint, "https://a.example/e.mp4", null, null, "Show", 1, 1, emptyMap(), emptyList())

    private fun batch(hint: String? = null) = IncomingRequest.Batch(
        hint, "Show", 1, null, emptyMap(),
        listOf(BatchEpisode(1, null, "https://a.example/e.mp4", emptyMap(), emptyList())),
    )

    private fun pick(r: IncomingRequest, movieDef: String? = null, tvDef: String? = null, cats: List<CategoryOption> = categories) =
        CategoryPreselector.preselect(r, cats, movieDef, tvDef)

    @Test
    fun rule1HintMatchingIdWins() {
        assertEquals("anime", pick(episode("anime"), movieDef = "movies", tvDef = "shows"))
        assertEquals("movies4k", pick(movie("movies4k"), movieDef = "movies"))
        assertEquals("anime", pick(batch("ANIME")))
    }

    @Test
    fun rule2SeasonAndEpisodeWithoutShowNameStillUsesDefaultTv() {
        val r = IncomingRequest.Single(null, "https://a.example/e.mp4", "X", null, null, 1, 2, emptyMap(), emptyList())
        assertEquals("anime", pick(r, movieDef = "movies", tvDef = "anime"))
        assertEquals("shows", pick(r, movieDef = "movies"))
    }

    @Test
    fun rule2EpisodicUsesDefaultTv() {
        assertEquals("anime", pick(episode("unknown"), movieDef = "movies", tvDef = "anime"))
        assertEquals("anime", pick(batch(), tvDef = "anime"))
    }

    @Test
    fun rule3NonEpisodicUsesDefaultMovie() {
        assertEquals("movies4k", pick(movie("unknown"), movieDef = "movies4k", tvDef = "shows"))
        assertEquals("flat1", pick(movie(), movieDef = "flat1"))
    }

    @Test
    fun rule4FirstFittingLayout() {
        assertEquals("shows", pick(episode()))
        assertEquals("flat1", pick(movie()))
        assertEquals("movies", pick(movie(), cats = categories.drop(1)))
    }

    @Test
    fun invalidDefaultsAreSkipped() {
        assertEquals("shows", pick(episode(), tvDef = "gone"))
        assertEquals("shows", pick(episode(), tvDef = "movies"))
        assertEquals("flat1", pick(movie(), movieDef = "shows"))
        assertEquals("flat1", pick(movie(), movieDef = "gone"))
    }

    @Test
    fun noFittingCategoryGivesNull() {
        assertNull(pick(episode(), cats = categories.take(3)))
        assertNull(pick(movie(), cats = categories.drop(3)))
        assertNull(pick(movie(), cats = emptyList()))
    }
}
