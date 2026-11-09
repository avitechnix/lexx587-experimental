import Toybox.Graphics;
import Toybox.Lang;
import Toybox.Math;

// Vector icons, so they stay sharp and take any color on both watch sizes.
// Each draws centered on (x, y) and fits a box roughly s pixels wide.
module Icons {

    function padlock(dc as Graphics.Dc, x as Number, y as Number, s as Number, open as Boolean) as Void {
        var bodyW = s * 7 / 10;
        var bodyH = s * 11 / 20;
        var bodyTop = y - s / 20;
        dc.fillRoundedRectangle(x - bodyW / 2, bodyTop, bodyW, bodyH, s / 10);

        var pen = s / 9 > 2 ? s / 9 : 2;
        var r = s * 9 / 40;
        var lift = open ? s / 5 : 0;
        // An open lock's shackle swings up and to the right.
        var sx = open ? x + r : x;
        var arcY = bodyTop - s / 5 - lift;
        dc.setPenWidth(pen);
        dc.drawArc(sx, arcY, r, Graphics.ARC_COUNTER_CLOCKWISE, 0, 180);
        dc.drawLine(sx + r, arcY, sx + r, bodyTop + 1);
        dc.drawLine(sx - r, arcY, sx - r, open ? arcY + s / 10 : bodyTop + 1);
        dc.setPenWidth(1);

        dc.setColor(Graphics.COLOR_BLACK, Graphics.COLOR_TRANSPARENT);
        dc.fillCircle(x, bodyTop + bodyH * 2 / 5, s / 14 > 1 ? s / 14 : 1);
    }

    // Three curved fan blades around a hub.
    function fan(dc as Graphics.Dc, x as Number, y as Number, s as Number) as Void {
        var r = s / 2;
        for (var i = 0; i < 3; i++) {
            var a = Math.toRadians(90 + i * 120);
            dc.fillPolygon([
                point(x, y, a, 0),
                point(x, y, a - 0.75, r * 0.5),
                point(x, y, a - 0.4, r * 0.95),
                point(x, y, a + 0.05, r * 1.0),
                point(x, y, a + 0.35, r * 0.75),
                point(x, y, a + 0.35, r * 0.3)
            ]);
        }
        dc.fillCircle(x, y, s / 8);
        dc.setColor(Graphics.COLOR_BLACK, Graphics.COLOR_TRANSPARENT);
        dc.fillCircle(x, y, s / 18 > 1 ? s / 18 : 1);
    }

    // A car seen from above, front up, with the frunk or trunk lid filled in.
    function car(dc as Graphics.Dc, x as Number, y as Number, s as Number, front as Boolean) as Void {
        var w = s * 3 / 5;
        var h = s;
        var left = x - w / 2;
        var top = y - h / 2;
        var radius = w * 2 / 5;
        var pen = s / 12 > 2 ? s / 12 : 2;

        dc.setPenWidth(pen);
        dc.drawRoundedRectangle(left, top, w, h, radius);
        // Windshield and rear window.
        dc.drawLine(left + pen, top + h * 2 / 5, left + w - pen, top + h * 2 / 5);
        dc.drawLine(left + pen, top + h * 3 / 4, left + w - pen, top + h * 3 / 4);
        dc.setPenWidth(1);

        var lidH = h / 4;
        dc.fillRoundedRectangle(left, front ? top : top + h - lidH - h / 20, w, lidH + h / 20, radius / 2);
    }

    // Small lightning bolt, for charging.
    function bolt(dc as Graphics.Dc, x as Number, y as Number, s as Number) as Void {
        dc.fillPolygon([
            [x + s * 0.15, y - s * 0.5],
            [x - s * 0.3, y + s * 0.08],
            [x - s * 0.02, y + s * 0.08],
            [x - s * 0.15, y + s * 0.5],
            [x + s * 0.3, y - s * 0.08],
            [x + s * 0.02, y - s * 0.08]
        ]);
    }

    function point(x as Number, y as Number, angle as Float or Double, r as Float or Double or Number) as [Numeric, Numeric] {
        return [x + r * Math.cos(angle), y - r * Math.sin(angle)];
    }
}
