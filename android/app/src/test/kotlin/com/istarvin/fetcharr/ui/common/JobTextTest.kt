package com.istarvin.fetcharr.ui.common

import org.junit.Assert.assertEquals
import org.junit.Test

class JobTextTest {
    @Test
    fun sceneNamesBecomeTitles() {
        assertEquals(
            "Spider-Man Brand New Day",
            tidyReleaseName("Spider-Man.Brand.New.Day.2026.2160p.MA.WEB-DL.Multi.DDP5.1.Atmos.x265"),
        )
        assertEquals("The Movie", tidyReleaseName("The_Movie_1080p_BluRay"))
    }

    @Test
    fun ordinaryTitlesAreUntouched() {
        assertEquals("Dune Part Two", tidyReleaseName("Dune Part Two"))
        assertEquals("Mr. Robot", tidyReleaseName("Mr. Robot"))
        assertEquals("v1.2", tidyReleaseName("v1.2"))
    }
}
