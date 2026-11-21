package dev.scolastico.omo.notifications

import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk=[35])
class BoardBasicAuthTest {
    @Test fun ipv6_challenges_accept_brackets_and_equivalent_numeric_addresses() {
        val origin="http://[::1]:8090/"
        for(host in listOf("::1","[::1]","0:0:0:0:0:0:0:1","[0:0:0:0:0:0:0:1]")) {
            assertTrue(host,boardChallengeAllowed(origin,origin,host,"omo"))
        }
        assertTrue(boardChallengeAllowed(origin,"http://[0:0:0:0:0:0:0:1]:8090/", "::1","omo"))
        assertTrue(boardChallengeAllowed("https://BOARD.example/","https://board.example/","BOARD.EXAMPLE","omo"))
    }
    @Test fun host_normalization_never_relaxes_origin_or_realm_checks() {
        val origin="http://[::1]:8090/"
        assertFalse(boardChallengeAllowed(origin,origin,"::2","omo"))
        assertFalse(boardChallengeAllowed(origin,"http://[::2]:8090/","::1","omo"))
        assertFalse(boardChallengeAllowed(origin,"https://[::1]:8090/","::1","omo"))
        assertFalse(boardChallengeAllowed(origin,"http://[::1]:8091/","::1","omo"))
        assertFalse(boardChallengeAllowed(origin,origin,"::1","other"))
        assertFalse(boardChallengeAllowed(origin,origin,"invalid::address","omo"))
    }
}
