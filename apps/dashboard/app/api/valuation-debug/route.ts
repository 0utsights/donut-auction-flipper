import { NextRequest, NextResponse } from "next/server";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  const backend = process.env.DN_BACKEND_URL || "http://localhost:8080";
  const token = process.env.DN_ADMIN_TOKEN || (process.env.NODE_ENV === "development" ? "local-admin-token" : "");
  const signature = request.nextUrl.searchParams.get("signature")?.trim() || "";
  if (!token) {
    return NextResponse.json({ error: "dashboard admin token is not configured" }, { status: 503 });
  }
  if (!signature || signature.length > 2048) {
    return NextResponse.json({ error: "signature is required" }, { status: 400 });
  }
  try {
    const response = await fetch(`${backend}/api/v1/debug/valuation?signature=${encodeURIComponent(signature)}`, {
      cache: "no-store",
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!response.ok) {
      return NextResponse.json({ error: "backend rejected debug request" }, { status: response.status });
    }
    return NextResponse.json(await response.json(), { headers: { "Cache-Control": "no-store" } });
  } catch {
    return NextResponse.json({ error: "backend unavailable" }, { status: 502 });
  }
}
