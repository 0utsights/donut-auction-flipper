package net.donutnetwork.client;

import net.minecraft.scoreboard.Scoreboard;
import net.minecraft.scoreboard.ScoreboardCriterion;
import net.minecraft.scoreboard.ScoreboardDisplaySlot;
import net.minecraft.scoreboard.ScoreboardObjective;
import net.minecraft.scoreboard.Team;
import net.minecraft.text.Text;
import net.minecraft.util.Formatting;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertSame;

class DonutNetworkClientTest {
    @Test void followsTheHudTeamColoredSidebarBeforeTheDefaultSidebar() {
        Scoreboard scoreboard = new Scoreboard();
        ScoreboardObjective fallback = objective(scoreboard, "fallback");
        ScoreboardObjective red = objective(scoreboard, "red_sidebar");
        scoreboard.setObjectiveSlot(ScoreboardDisplaySlot.SIDEBAR, fallback);
        Team team = scoreboard.addTeam("red_team");
        team.setColor(Formatting.RED);
        scoreboard.addScoreHolderToTeam("Outsights", team);
        scoreboard.setObjectiveSlot(ScoreboardDisplaySlot.fromFormatting(Formatting.RED), red);

        assertSame(red, DonutNetworkClient.visibleSidebarObjective(scoreboard, "Outsights"));
        assertSame(fallback, DonutNetworkClient.visibleSidebarObjective(scoreboard, "SomeoneElse"));
    }

    private static ScoreboardObjective objective(Scoreboard scoreboard, String name) {
        return scoreboard.addObjective(name, ScoreboardCriterion.DUMMY, Text.literal(name),
                ScoreboardCriterion.RenderType.INTEGER, false, null);
    }

    @Test void boundsLocalSidebarDiagnostics() {
        String summary = DonutNetworkClient.summarizeSidebarRows(java.util.List.of("$", "142M\u0080", "x".repeat(2_000)));
        org.junit.jupiter.api.Assertions.assertTrue(summary.startsWith("[$] | [142M<U+0080>]"));
        org.junit.jupiter.api.Assertions.assertTrue(summary.length() <= 1_200);
    }
}
