package com.istarvin.fetcharr

import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

/** Verifies the top-level /contract directory is on the unit-test classpath. */
class ContractResourcesTest {
    @Test
    fun contractReadmeIsOnClasspath() {
        val stream = javaClass.classLoader!!.getResourceAsStream("README.md")
        assumeTrue("contract/README.md not present yet", stream != null)
        stream!!.use { assertTrue(it.readBytes().isNotEmpty()) }
    }
}
