package com.istarvin.fetcharr.data.settings

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class UrlNormalizerTest {
    private fun n(s: String) = UrlNormalizer.normalize(s)

    @Test
    fun addsHttpWhenNoScheme() {
        assertEquals("http://192.168.1.50:8080", n("192.168.1.50:8080"))
        assertEquals("http://localhost:8080", n("localhost:8080"))
        assertEquals("http://fetcharr.lan", n("fetcharr.lan"))
    }

    @Test
    fun keepsExistingScheme() {
        assertEquals("https://fetcharr.example.com", n("https://fetcharr.example.com"))
        assertEquals("http://host:1", n("http://host:1"))
    }

    @Test
    fun trimsWhitespace() {
        assertEquals("http://host:8080", n("  http://host:8080 \n"))
    }

    @Test
    fun removesTrailingSlashes() {
        assertEquals("http://host:8080", n("http://host:8080/"))
        assertEquals("http://host:8080", n("host:8080///"))
        assertEquals("https://host/fetcharr", n("https://host/fetcharr/"))
    }

    @Test
    fun blankIsNull() {
        assertNull(n(""))
        assertNull(n("   "))
    }

    @Test
    fun invalidIsNull() {
        assertNull(n("http://"))
        assertNull(n("://host"))
        assertNull(n("ftp://host"))
        assertNull(n("http://exa mple.com"))
        assertNull(n("http://host:99999"))
    }

    @Test
    fun idempotent() {
        val once = n("Host:8080/")!!
        assertEquals(once, n(once))
    }
}
