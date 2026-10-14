package com.istarvin.fetcharr.data.sync

import org.junit.Assert.assertEquals
import org.junit.Test

class BackoffPolicyTest {
    @Test
    fun scheduleDoublesAndCapsAtThirtySeconds() {
        val policy = BackoffPolicy()
        val delays = List(9) { policy.next() }
        assertEquals(listOf(1_000L, 2_000L, 4_000L, 8_000L, 16_000L, 30_000L, 30_000L, 30_000L, 30_000L), delays)
    }

    @Test
    fun resetStartsOver() {
        val policy = BackoffPolicy()
        repeat(4) { policy.next() }
        policy.reset()
        assertEquals(1_000L, policy.next())
        assertEquals(2_000L, policy.next())
    }
}
